// SPDX-License-Identifier: GPL-3.0-or-later

package artifact

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"os"
	"path/filepath"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
)

// Register mounts the M3 artifact download route on mux:
//
//	GET /api/artifacts/{id}
//
// The route is meant to be mounted inside the authenticated API mux (the API
// layer wraps it with sessions, security headers and trace ids). The handler
// streams the file, sets Content-Disposition attachment and Cache-Control
// no-store, and answers unknown/expired artifacts with the documented
// four-field JSON 404 ({code,message,details,traceId}).
func (s *Store) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/artifacts/{id}", s.handleDownload)
}

// handleDownload serves GET /api/artifacts/{id}.
func (s *Store) handleDownload(w http.ResponseWriter, r *http.Request) {
	art, ok := s.Get(r.PathValue("id"), s.now())
	if !ok {
		writeNotFoundError(w)
		return
	}
	file, info, err := s.openArtifact(art)
	if err != nil {
		// Missing, replaced or escaping files are indistinguishable from an
		// expired artifact from the client's point of view.
		writeNotFoundError(w)
		return
	}
	defer file.Close()

	header := w.Header()
	header.Set("Content-Type", art.ContentType)
	header.Set("Cache-Control", "no-store")
	if disposition := mime.FormatMediaType("attachment", map[string]string{"filename": art.Filename}); disposition != "" {
		header.Set("Content-Disposition", disposition)
	} else {
		header.Set("Content-Disposition", "attachment")
	}
	// ServeContent streams the file (and handles HEAD/range requests) without
	// loading it into memory.
	http.ServeContent(w, r, art.Filename, info.ModTime(), file)
}

// openArtifact opens the artifact file after re-validating that it is still a
// regular file whose real path stays inside the store directory.
func (s *Store) openArtifact(art Artifact) (*os.File, os.FileInfo, error) {
	realPath, err := filepath.EvalSymlinks(art.Path)
	if err != nil {
		return nil, nil, err
	}
	if !s.containsRealPath(realPath) {
		return nil, nil, errors.New("artifact: 路径不在存储目录内")
	}
	file, err := os.Open(realPath)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, nil, errors.New("artifact: 不是普通文件")
	}
	return file, info, nil
}

// errorBody is the documented M1/M3 JSON error body
// (docs/IMPLEMENTATION_SPEC.md `4.4): all four fields are always present and
// details is always an object.
type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details"`
	TraceID string `json:"traceId"`
}

// writeNotFoundError writes a 404 NOT_FOUND JSON error with details {} and the
// X-Trace-Id response header value (set by the API tracing middleware).
func writeNotFoundError(w http.ResponseWriter) {
	apiErr := apierr.NotFound("文件不存在或已过期。")
	body, err := json.Marshal(errorBody{
		Code:    apiErr.Code,
		Message: apiErr.Message,
		Details: map[string]any{},
		TraceID: w.Header().Get("X-Trace-Id"),
	})
	if err != nil {
		body = []byte(`{"code":"INTERNAL","message":"响应编码失败。","details":{},"traceId":""}`)
	}
	header := w.Header()
	header.Set("Content-Type", "application/json; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write(body)
}
