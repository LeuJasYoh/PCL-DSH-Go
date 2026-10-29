// SPDX-License-Identifier: MIT

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	// DefaultRegistry is the npm registry used when settings carry no value.
	DefaultRegistry = "https://registry.npmjs.org"
	// DSHPackage is the npm package that carries the DeepSeek Harness CLI.
	DSHPackage = "@deepseek-ai/dsh"

	// registryQueryTimeout bounds a whole registry metadata request
	// (docs/DSH_INTEGRATION.md section 2).
	registryQueryTimeout = 60 * time.Second
	// maxRegistryBodyBytes caps a registry response before it is parsed.
	maxRegistryBodyBytes = 32 << 20
	// registryUserAgent identifies the manager to npm registries.
	registryUserAgent = "pcl-dsh-go"
)

var (
	// ErrUpstream marks registry responses that are HTTP-non-2xx, oversized or
	// unparsable. The HTTP layer maps it to 502 UPSTREAM_ERROR.
	ErrUpstream = errors.New("上游 npm 服务返回错误")
	// ErrUpstreamTimeout marks network failures (timeouts and connection
	// errors). The HTTP layer maps it to 504 UPSTREAM_TIMEOUT.
	ErrUpstreamTimeout = errors.New("上游 npm 服务连接失败")
	// ErrInvalidRegistry marks a registry value that is not a plain http(s) URL.
	ErrInvalidRegistry = errors.New("npm 源地址无效")
)

var (
	packageNamePattern = regexp.MustCompile(`^@[a-z0-9][a-z0-9._-]*/[a-z0-9][a-z0-9._-]*$`)
	versionPathPattern = regexp.MustCompile(`^[0-9A-Za-z.+-]+$`)
)

// registryDocument is the subset of the npm packument consumed by version
// discovery.
type registryDocument struct {
	Name     string                     `json:"name"`
	Versions map[string]json.RawMessage `json:"versions"`
}

// packageManifest is the subset of an npm version manifest used for installs and
// release-cohort collection.
type packageManifest struct {
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	Dependencies         map[string]string `json:"dependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
}

// newRegistryHTTPClient builds the HTTP client used for registry metadata. It
// never follows redirects, honours the configured proxy (or the process
// environment when none is configured) and bounds each request by the 60s
// registry timeout.
func newRegistryHTTPClient(proxy string) *http.Client {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if proxy != "" {
		if parsed, err := url.Parse(proxy); err == nil && parsed.Scheme != "" && parsed.Host != "" {
			transport.Proxy = http.ProxyURL(parsed)
		}
	}
	return &http.Client{
		Timeout:   registryQueryTimeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			// docs/DSH_INTEGRATION.md section 2: redirects are disabled. The
			// 3xx response is then rejected as a non-2xx upstream error.
			return http.ErrUseLastResponse
		},
	}
}

// normalizeRegistry validates an npm registry base URL and strips trailing
// slashes. An empty value selects DefaultRegistry.
func normalizeRegistry(registry string) (string, error) {
	trimmed := strings.TrimSpace(registry)
	if trimmed == "" {
		return DefaultRegistry, nil
	}
	if containsASCIIWhitespace(trimmed) {
		return "", fmt.Errorf("%w：地址不能包含空白字符", ErrInvalidRegistry)
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("%w：%v", ErrInvalidRegistry, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("%w：只支持 http(s) 地址", ErrInvalidRegistry)
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return "", fmt.Errorf("%w：缺少主机名", ErrInvalidRegistry)
	}
	if parsed.User != nil {
		return "", fmt.Errorf("%w：地址不能包含用户名或密码", ErrInvalidRegistry)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("%w：地址不能包含查询串或片段", ErrInvalidRegistry)
	}
	return strings.TrimRight(trimmed, "/"), nil
}

// containsASCIIWhitespace reports whether the string contains an ASCII space,
// tab, carriage return or line feed (registry URLs must not contain any).
func containsASCIIWhitespace(text string) bool {
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case ' ', 0x09, 0x0d, 0x0a:
			return true
		}
	}
	return false
}

// escapedPackagePath renders an npm package name as a registry path segment,
// encoding the scope separator as %2f (registry convention).
func escapedPackagePath(packageName string) string {
	return strings.ReplaceAll(packageName, "/", "%2f")
}

func validPackageName(packageName string) bool {
	return len(packageName) <= 214 && packageNamePattern.MatchString(packageName)
}

// registryGetJSON performs one bounded GET against the registry and decodes the
// response body into out.
func registryGetJSON(ctx context.Context, client *http.Client, endpoint string, maxBytes int64, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("%w：%v", ErrInvalidRegistry, err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", registryUserAgent)
	response, err := client.Do(request)
	if err != nil {
		return classifyUpstreamTransportError(ctx, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		_ = response.Body.Close()
	}()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("%w：npm 返回 HTTP %d，请检查下载源或代理后重试", ErrUpstream, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return classifyUpstreamTransportError(ctx, err)
	}
	if int64(len(body)) > maxBytes {
		return fmt.Errorf("%w：npm 返回的数据超过 %d 字节上限", ErrUpstream, maxBytes)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%w：npm 返回的 JSON 无法解析：%v", ErrUpstream, err)
	}
	return nil
}

// classifyUpstreamTransportError maps network failures to the two error
// classes required by the HTTP contract: 504 for timeouts/connection failures,
// a cancellation error when the caller gave up.
func classifyUpstreamTransportError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return fmt.Errorf("%w：npm 查询超时，请检查下载源或代理后重试", ErrUpstreamTimeout)
		}
		return fmt.Errorf("npm 查询已取消：%w", ctxErr)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("%w：npm 查询超时，请检查下载源或代理后重试", ErrUpstreamTimeout)
	}
	return fmt.Errorf("%w：无法连接 npm 源（%v），请检查网络或代理后重试", ErrUpstreamTimeout, err)
}

// fetchAvailableVersions queries the packument for DSHPackage and returns the
// valid SemVer versions in descending precedence order.
func fetchAvailableVersions(ctx context.Context, client *http.Client, registry string) ([]string, error) {
	base, err := normalizeRegistry(registry)
	if err != nil {
		return nil, err
	}
	endpoint := base + "/" + escapedPackagePath(DSHPackage)
	var document registryDocument
	if err := registryGetJSON(ctx, client, endpoint, maxRegistryBodyBytes, &document); err != nil {
		return nil, err
	}
	if document.Versions == nil {
		return nil, fmt.Errorf("%w：npm 返回的版本清单格式无效", ErrUpstream)
	}
	keys := make([]string, 0, len(document.Versions))
	for version := range document.Versions {
		keys = append(keys, version)
	}
	versions := SortVersionsDescending(keys)
	if len(versions) == 0 {
		return nil, fmt.Errorf("%w：npm 源没有返回可用的 Harness 版本", ErrUpstream)
	}
	return versions, nil
}

// fetchPackageManifest reads the version manifest for one package from the
// registry and verifies that the registry echoed the requested name/version.
func fetchPackageManifest(ctx context.Context, client *http.Client, registry, packageName, version string) (packageManifest, error) {
	if !validPackageName(packageName) {
		return packageManifest{}, fmt.Errorf("npm 包名无效：%q", packageName)
	}
	if !IsVersion(version) {
		return packageManifest{}, fmt.Errorf("版本号 %q 无效", version)
	}
	if !versionPathPattern.MatchString(version) {
		return packageManifest{}, fmt.Errorf("版本号 %q 不能用作 registry 路径", version)
	}
	base, err := normalizeRegistry(registry)
	if err != nil {
		return packageManifest{}, err
	}
	endpoint := base + "/" + escapedPackagePath(packageName) + "/" + version
	var manifest packageManifest
	if err := registryGetJSON(ctx, client, endpoint, maxRegistryBodyBytes, &manifest); err != nil {
		return packageManifest{}, err
	}
	if manifest.Name != packageName || manifest.Version != version {
		return packageManifest{}, fmt.Errorf("%w：%s@%s 的发行清单包名或版本不匹配", ErrUpstream, packageName, version)
	}
	return manifest, nil
}
