// SPDX-License-Identifier: MIT

// Package pack implements the M3 integration-package service: format detection,
// preview, and (import.go/export.go) safe restore and export of .dspack v2/v3
// and Overture snapshot v1 ZIP archives.
//
// Contract: docs/M3_INTERFACES.md §3 and docs/DSH_INTEGRATION.md §7.
// Archive contents are never executed: detection/preview only read bounded
// metadata JSON/YAML, and every archive goes through internal/safe first.
package pack

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/artifact"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/config"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/runtime"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/safe"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
)

const (
	// previewTTL is how long an uploaded archive can be imported
	// (docs/DSH_INTEGRATION.md §7.3: 30 minutes).
	previewTTL = 30 * time.Minute
	// previewIDBytes is the random part of a preview id (12 hex characters).
	previewIDBytes = 6
	// maxPreviewNameBytes bounds a preview display name.
	maxPreviewNameBytes = 160
	// maxProfileYAMLBytes bounds one Overture profile.yaml read (reference impl).
	maxProfileYAMLBytes = 256 * 1024
	// uploadBufferBytes is the streaming upload buffer size.
	uploadBufferBytes = 256 * 1024
)

// previewIDPattern matches a canonical preview id (12 lowercase hex digits).
var previewIDPattern = regexp.MustCompile("^[0-9a-f]{12}$")

// Format identifies a supported integration-package format
// (docs/M3_INTERFACES.md §3).
type Format string

const (
	// FormatDSPack is the .dspack ZIP container (dspack.json + manifest.json).
	FormatDSPack Format = "dspack"
	// FormatOverture is an Overture snapshot ZIP v1 (dsh-snapshot.json).
	FormatOverture Format = "overture"
)

// Preview is a validated, not-yet-imported integration package
// (docs/M3_INTERFACES.md §3). CreatedAt/ExpiresAt are wall-clock times.
type Preview struct {
	ID            string
	Name          string
	Format        Format
	Version       string
	DSHVersion    string
	Profiles      []string
	UnpackedBytes int64
	Warnings      []string
	CreatedAt     time.Time
	ExpiresAt     time.Time
}

// ImportInput is the request to import a previously previewed package.
type ImportInput struct {
	PreviewID string
	Name      string
	Version   string
}

// ImportResult is the outcome of a successful import.
type ImportResult struct {
	InstanceID string
	Name       string
}

// ExportInput is the request to export an instance profile or its whole home.
type ExportInput struct {
	InstanceID string
	Mode       string // "profile" | "home"
}

// ExportResult is the outcome of a successful export.
type ExportResult struct {
	ArtifactID string
	Filename   string
	Size       int64
}

// RuntimeAccess is the subset of *runtime.Service used by pack import.
type RuntimeAccess interface {
	Installed(version string) bool
	Install(ctx context.Context, version string, logf func(string)) (runtime.InstallResult, error)
}

// AppAccess is the subset of *app.App used by pack import.
type AppAccess interface {
	CreateInstance(ctx context.Context, in store.CreateInstanceInput) (store.Instance, error)
	DeleteInstance(ctx context.Context, instanceID string) (string, error)
	RestoreProfileDependencies(ctx context.Context, instanceID, profile string, logf func(string)) error
	EntryPath(version string) (string, error)
}

// ArtifactStore registers exported archives for download (internal/artifact).
type ArtifactStore interface {
	Add(filename, path, contentType string, size int64) (artifact.Artifact, error)
}

// Options are the pack service dependencies (docs/M3_INTERFACES.md §3).
type Options struct {
	DataRoot  string
	Dirs      config.Dirs
	Store     *store.Store
	Safe      safe.Limits
	Runtime   RuntimeAccess
	App       AppAccess
	Artifacts ArtifactStore
	Logger    *slog.Logger
}

// previewRecord is the in-memory state of one uploaded archive.
type previewRecord struct {
	preview       Preview
	archivePath   string
	archiveSHA256 string
	archiveBytes  int64
}

// Service implements integration-package preview/import/export. It is safe for
// concurrent use.
type Service struct {
	dataRoot  string
	dirs      config.Dirs
	store     *store.Store
	limits    safe.Limits
	runtime   RuntimeAccess
	app       AppAccess
	artifacts ArtifactStore
	logger    *slog.Logger

	// now is the clock used for preview TTL decisions; tests replace it.
	now func() time.Time
	// httpClient downloads manifest-declared external resources; tests replace
	// it with an httptest TLS client.
	httpClient *http.Client

	mu       sync.Mutex
	previews map[string]*previewRecord
}

// New validates dependencies and creates the pack service. A zero Safe value
// means safe.DefaultLimits().
func New(opts Options) (*Service, error) {
	if opts.Logger == nil {
		return nil, errors.New("pack: logger is required")
	}
	if opts.Store == nil {
		return nil, errors.New("pack: store is required")
	}
	if opts.Runtime == nil {
		return nil, errors.New("pack: runtime access is required")
	}
	if opts.App == nil {
		return nil, errors.New("pack: app access is required")
	}
	if opts.Artifacts == nil {
		return nil, errors.New("pack: artifact store is required")
	}
	dataRoot := strings.TrimSpace(opts.Dirs.DataRoot)
	if dataRoot == "" {
		dataRoot = strings.TrimSpace(opts.DataRoot)
	}
	if dataRoot == "" {
		return nil, errors.New("pack: data root is required")
	}
	absolute, err := filepath.Abs(dataRoot)
	if err != nil {
		return nil, fmt.Errorf("pack: 解析 data root 失败: %w", err)
	}
	dirs := opts.Dirs
	dirs.DataRoot = absolute
	if dirs.InstancesDir == "" {
		dirs.InstancesDir = filepath.Join(absolute, "instances")
	}
	if dirs.CacheDir == "" {
		dirs.CacheDir = filepath.Join(absolute, "cache")
	}
	if dirs.TrashDir == "" {
		dirs.TrashDir = filepath.Join(absolute, "trash")
	}
	if dirs.LogsDir == "" {
		dirs.LogsDir = filepath.Join(absolute, "logs")
	}
	for _, dir := range []string{dirs.InstancesDir, dirs.CacheDir, dirs.TrashDir} {
		if !filepath.IsAbs(dir) {
			return nil, fmt.Errorf("pack: 目录必须是绝对路径: %s", dir)
		}
	}
	uploadsDir := filepath.Join(dirs.CacheDir, "uploads")
	if err := os.MkdirAll(uploadsDir, 0o700); err != nil {
		return nil, fmt.Errorf("pack: 创建上传目录 %s 失败: %w", uploadsDir, err)
	}
	return &Service{
		dataRoot:   absolute,
		dirs:       dirs,
		store:      opts.Store,
		limits:     effectiveLimits(opts.Safe),
		runtime:    opts.Runtime,
		app:        opts.App,
		artifacts:  opts.Artifacts,
		logger:     opts.Logger,
		now:        time.Now,
		httpClient: newPackHTTPClient(opts.Store.Settings().Proxy),
		previews:   make(map[string]*previewRecord),
	}, nil
}

// newPackHTTPClient builds the client used to download manifest-declared
// external resources: HTTPS only (enforced again by CheckRedirect), at most 5
// redirects, configured proxy from manager settings.
func newPackHTTPClient(proxyValue string) *http.Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
	}
	if cleaned := strings.TrimSpace(proxyValue); cleaned != "" {
		if parsed, err := url.Parse(cleaned); err == nil && parsed.Host != "" {
			transport.Proxy = http.ProxyURL(parsed)
		}
	}
	return &http.Client{
		Transport: transport,
		Timeout:   5 * time.Minute,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("资源下载重定向过多")
			}
			return validateResourceURL(request.URL.String())
		},
	}
}

// effectiveLimits normalises a limits value the same way internal/safe does, so
// pack can use the numbers before calling into safe.
func effectiveLimits(limits safe.Limits) safe.Limits {
	defaults := safe.DefaultLimits()
	if limits.MaxArchiveBytes <= 0 {
		limits.MaxArchiveBytes = defaults.MaxArchiveBytes
	}
	if limits.MaxUnpackedBytes <= 0 {
		limits.MaxUnpackedBytes = defaults.MaxUnpackedBytes
	}
	if limits.MaxFileBytes <= 0 {
		limits.MaxFileBytes = defaults.MaxFileBytes
	}
	if limits.MaxMetadataBytes <= 0 {
		limits.MaxMetadataBytes = defaults.MaxMetadataBytes
	}
	if limits.MaxEntries <= 0 {
		limits.MaxEntries = defaults.MaxEntries
	}
	if limits.MaxPathLength <= 0 {
		limits.MaxPathLength = defaults.MaxPathLength
	}
	if limits.MaxCompressionRatio <= 0 {
		limits.MaxCompressionRatio = defaults.MaxCompressionRatio
	}
	return limits
}

// uploadsDir is <cache>/uploads for streamed preview uploads.
func (s *Service) uploadsDir() string { return filepath.Join(s.dirs.CacheDir, "uploads") }

// stagingDir is <cache>/pack-staging for import extraction (import.go).
func (s *Service) stagingDir() string { return filepath.Join(s.dirs.CacheDir, "pack-staging") }

// exportsDir is <cache>/exports where generated archives are written.
func (s *Service) exportsDir() string { return filepath.Join(s.dirs.CacheDir, "exports") }

func newPreviewID() (string, error) {
	var raw [previewIDBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("生成预览 ID 失败: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

// --- errors ---------------------------------------------------------------

func validationError(message string) error { return apierr.Validation(message) }

func notFoundError(message string) error { return apierr.NotFound(message) }

func conflictError(code, message string) error { return apierr.New(409, code, message) }

func internalError(message string) error { return apierr.New(500, apierr.CodeInternal, message) }

func tooLargeError(message string) error {
	return apierr.New(413, apierr.CodePayloadTooLarge, message)
}
