// SPDX-License-Identifier: MIT

package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAvailableVersionsSuccess(t *testing.T) {
	t.Parallel()
	var escapedPath atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		escapedPath.Store(r.URL.EscapedPath())
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"name":"@deepseek-ai/dsh","versions":{"0.2.1-rc.3":{},"0.2.1":{},"0.2.1-rc.10":{},"0.1.0":{},"latest":{},"next":{}}}`)
	}))
	defer server.Close()

	versions, err := fetchAvailableVersions(context.Background(), newRegistryHTTPClient(""), server.URL)
	if err != nil {
		t.Fatalf("fetchAvailableVersions() error = %v", err)
	}
	want := []string{"0.2.1", "0.2.1-rc.10", "0.2.1-rc.3", "0.1.0"}
	if fmt.Sprint(versions) != fmt.Sprint(want) {
		t.Fatalf("fetchAvailableVersions() = %v, want %v", versions, want)
	}
	if got := escapedPath.Load(); got != "/@deepseek-ai%2fdsh" {
		t.Fatalf("request escaped path = %q, want /@deepseek-ai%%2fdsh", got)
	}
}

func TestAvailableVersionsUpstreamHTTPErrors(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusNotFound, http.StatusInternalServerError, http.StatusBadGateway} {
		status := status
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			}))
			defer server.Close()
			_, err := fetchAvailableVersions(context.Background(), newRegistryHTTPClient(""), server.URL)
			if !errors.Is(err, ErrUpstream) {
				t.Fatalf("error = %v, want ErrUpstream", err)
			}
			if errors.Is(err, ErrUpstreamTimeout) {
				t.Fatalf("error = %v, must not be classified as timeout", err)
			}
		})
	}
}

func TestAvailableVersionsInvalidJSON(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "this is not json")
	}))
	defer server.Close()
	_, err := fetchAvailableVersions(context.Background(), newRegistryHTTPClient(""), server.URL)
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("error = %v, want ErrUpstream", err)
	}
}

func TestAvailableVersionsNoVersions(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"name":"@deepseek-ai/dsh","versions":{}}`)
	}))
	defer server.Close()
	_, err := fetchAvailableVersions(context.Background(), newRegistryHTTPClient(""), server.URL)
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("error = %v, want ErrUpstream", err)
	}
}

func TestRegistryResponseBodyLimit(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("a", 128))
	}))
	defer server.Close()
	var out map[string]any
	err := registryGetJSON(context.Background(), newRegistryHTTPClient(""), server.URL, 64, &out)
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("error = %v, want ErrUpstream for oversized body", err)
	}
	if !strings.Contains(err.Error(), "上限") {
		t.Fatalf("error = %v, want an explicit size-limit message", err)
	}
}

func TestAvailableVersionsTimeout(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)

	client := newRegistryHTTPClient("")
	client.Timeout = 100 * time.Millisecond
	started := time.Now()
	_, err := fetchAvailableVersions(context.Background(), client, server.URL)
	if !errors.Is(err, ErrUpstreamTimeout) {
		t.Fatalf("error = %v, want ErrUpstreamTimeout", err)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatalf("timeout was not enforced promptly")
	}
}

func TestAvailableVersionsConnectionError(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	endpoint := "http://" + listener.Addr().String()
	_ = listener.Close()

	_, err = fetchAvailableVersions(context.Background(), newRegistryHTTPClient(""), endpoint)
	if !errors.Is(err, ErrUpstreamTimeout) {
		t.Fatalf("error = %v, want ErrUpstreamTimeout for connection failure", err)
	}
}

func TestRegistryRedirectsAreNotFollowed(t *testing.T) {
	t.Parallel()
	var redirected atomic.Int32
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			redirected.Add(1)
			_, _ = io.WriteString(w, `{"versions":{"1.0.0":{}}}`)
			return
		}
		http.Redirect(w, r, serverURL+"/elsewhere", http.StatusFound)
	}))
	defer server.Close()
	serverURL = server.URL

	_, err := fetchAvailableVersions(context.Background(), newRegistryHTTPClient(""), server.URL)
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("error = %v, want ErrUpstream for a redirect response", err)
	}
	if redirected.Load() != 0 {
		t.Fatalf("redirect target was requested; redirects must be disabled")
	}
}

func TestAvailableVersionsCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fetchAvailableVersions(ctx, newRegistryHTTPClient(""), "http://127.0.0.1:1")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if errors.Is(err, ErrUpstreamTimeout) {
		t.Fatalf("caller cancellation must not be reported as an upstream timeout")
	}
}

func TestNormalizeRegistry(t *testing.T) {
	t.Parallel()
	valid := []struct{ in, want string }{
		{"", DefaultRegistry},
		{"https://registry.npmjs.org", "https://registry.npmjs.org"},
		{"https://registry.npmjs.org/", "https://registry.npmjs.org"},
		{"http://127.0.0.1:4873", "http://127.0.0.1:4873"},
		{" http://127.0.0.1:4873/prefix/ ", "http://127.0.0.1:4873/prefix"},
	}
	for _, tc := range valid {
		got, err := normalizeRegistry(tc.in)
		if err != nil {
			t.Fatalf("normalizeRegistry(%q) error = %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("normalizeRegistry(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	invalid := []string{
		"ftp://registry.npmjs.org",
		"registry.npmjs.org",
		"https://user:pass@registry.npmjs.org",
		"https://registry.npmjs.org/?token=x",
		"https://registry.npmjs.org/#fragment",
		"https://registry.npmjs.org/a b",
	}
	for _, in := range invalid {
		_, err := normalizeRegistry(in)
		if !errors.Is(err, ErrInvalidRegistry) {
			t.Fatalf("normalizeRegistry(%q) error = %v, want ErrInvalidRegistry", in, err)
		}
	}
}

func TestEscapedPackagePathAndPackageNames(t *testing.T) {
	t.Parallel()
	if got, want := escapedPackagePath(DSHPackage), "@deepseek-ai%2fdsh"; got != want {
		t.Fatalf("escapedPackagePath(%q) = %q, want %q", DSHPackage, got, want)
	}
	if !validPackageName("@deepseek-ai/dsh-core") {
		t.Fatalf("validPackageName rejected a legitimate scoped package")
	}
	for _, name := range []string{"", "dsh", "@scope", "@scope/", "@/pkg", "@scope/Pkg", "@scope/pkg name", "../../etc/passwd", "@deepseek-ai/dsh%2f.."} {
		if validPackageName(name) {
			t.Fatalf("validPackageName(%q) = true, want false", name)
		}
	}
}

func TestFetchPackageManifest(t *testing.T) {
	t.Parallel()
	var gotPath atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath.Store(r.URL.EscapedPath())
		_, _ = io.WriteString(w, `{"name":"@deepseek-ai/dsh","version":"0.2.1-rc.3",
			"dependencies":{"@deepseek-ai/dsh-core":"0.2.1-rc.3"},
			"peerDependencies":{"@deepseek-ai/dsh-web":"^0.2.1-rc.3"},
			"optionalDependencies":{"@deepseek-ai/dsh-native":"~0.2.1-rc.3"}}`)
	}))
	defer server.Close()

	manifest, err := fetchPackageManifest(context.Background(), newRegistryHTTPClient(""), server.URL, DSHPackage, "0.2.1-rc.3")
	if err != nil {
		t.Fatalf("fetchPackageManifest() error = %v", err)
	}
	if manifest.Name != DSHPackage || manifest.Version != "0.2.1-rc.3" {
		t.Fatalf("manifest = %+v", manifest)
	}
	if manifest.Dependencies["@deepseek-ai/dsh-core"] != "0.2.1-rc.3" {
		t.Fatalf("dependencies = %v", manifest.Dependencies)
	}
	if manifest.PeerDependencies["@deepseek-ai/dsh-web"] != "^0.2.1-rc.3" {
		t.Fatalf("peerDependencies = %v", manifest.PeerDependencies)
	}
	if got := gotPath.Load(); got != "/@deepseek-ai%2fdsh/0.2.1-rc.3" {
		t.Fatalf("request escaped path = %q", got)
	}
}

func TestFetchPackageManifestMismatch(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"name":"@deepseek-ai/dsh-other","version":"0.2.1-rc.3"}`)
	}))
	defer server.Close()
	_, err := fetchPackageManifest(context.Background(), newRegistryHTTPClient(""), server.URL, DSHPackage, "0.2.1-rc.3")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("error = %v, want ErrUpstream for a name/version mismatch", err)
	}
}

func TestFetchPackageManifestValidation(t *testing.T) {
	t.Parallel()
	client := newRegistryHTTPClient("")
	if _, err := fetchPackageManifest(context.Background(), client, "http://127.0.0.1:1", "../../etc", "1.0.0"); err == nil {
		t.Fatalf("invalid package name was accepted")
	}
	if _, err := fetchPackageManifest(context.Background(), client, "http://127.0.0.1:1", DSHPackage, "latest"); err == nil {
		t.Fatalf("invalid version was accepted")
	}
}
