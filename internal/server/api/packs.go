// SPDX-License-Identifier: MIT

package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/pack"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/job"
)

// packPreviewDTO is the POST /api/packs/preview response body
// (docs/IMPLEMENTATION_SPEC.md 4.3 PackPreviewDto).
type packPreviewDTO struct {
	PreviewID     string   `json:"previewId"`
	Name          string   `json:"name"`
	Format        string   `json:"format"`
	Version       string   `json:"version"`
	DSHVersion    string   `json:"dshVersion"`
	Profiles      []string `json:"profiles"`
	UnpackedBytes int64    `json:"unpackedBytes"`
	Warnings      []string `json:"warnings"`
}

// packImportRequest is the POST /api/packs/import body.
type packImportRequest struct {
	PreviewID string `json:"previewId"`
	Name      string `json:"name"`
	Version   string `json:"version"`
}

// packExportRequest is the POST /api/packs/export body.
type packExportRequest struct {
	InstanceID string `json:"instanceId"`
	Mode       string `json:"mode"`
}

// importJobResult is the IMPORT_PACK job result (docs/IMPLEMENTATION_SPEC.md 4.3).
type importJobResult struct {
	InstanceID string `json:"instanceId"`
	Name       string `json:"name"`
}

// exportJobResult is the EXPORT_PACK job result.
type exportJobResult struct {
	ArtifactID string `json:"artifactId"`
	Filename   string `json:"filename"`
	Size       int64  `json:"size"`
}

// handlePackPreview serves POST /api/packs/preview: the archive is streamed to
// a temporary file (2 GiB limit), handed to pack.Preview and removed again.
// Preview does not occupy the global Job slot, but the busy gate applies.
func (a *API) handlePackPreview(w http.ResponseWriter, r *http.Request) {
	upload, err := receiveUpload(r.Context(), r, filepath.Join(a.dirs.CacheDir, "uploads"), maxPackUploadBytes)
	if err != nil {
		a.writeUploadError(w, r, err)
		return
	}
	defer func() { _ = os.Remove(upload.Path) }()

	file, err := os.Open(upload.Path)
	if err != nil {
		a.writeError(w, r, apierr.New(http.StatusInternalServerError, apierr.CodeInternal, "读取上传文件失败。"))
		return
	}
	defer func() { _ = file.Close() }()

	preview, err := a.pack.Preview(r.Context(), file, upload.Filename)
	if err != nil {
		if r.Context().Err() != nil {
			// The client went away (or the manager is shutting down).
			return
		}
		a.writeError(w, r, err)
		return
	}
	profiles := preview.Profiles
	if profiles == nil {
		profiles = []string{}
	}
	warnings := preview.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	writeJSON(w, http.StatusOK, packPreviewDTO{
		PreviewID:     preview.ID,
		Name:          preview.Name,
		Format:        string(preview.Format),
		Version:       preview.Version,
		DSHVersion:    preview.DSHVersion,
		Profiles:      profiles,
		UnpackedBytes: preview.UnpackedBytes,
		Warnings:      warnings,
	})
}

// handlePackImport serves POST /api/packs/import: 202 IMPORT_PACK job.
func (a *API) handlePackImport(w http.ResponseWriter, r *http.Request) {
	var request packImportRequest
	if err := decodeJSON(w, r, &request); err != nil {
		a.writeError(w, r, err)
		return
	}
	previewID := strings.TrimSpace(request.PreviewID)
	if previewID == "" {
		a.writeError(w, r, apierr.Validation("previewId 不能为空。"))
		return
	}
	view, err := a.jobs.Start(job.KindImportPack, 0, func(ctx context.Context, j *job.Job) (any, error) {
		j.Phase("导入整合包", "正在校验预览并恢复实例。")
		result, err := a.pack.Import(ctx, pack.ImportInput{
			PreviewID: previewID,
			Name:      strings.TrimSpace(request.Name),
			Version:   strings.TrimSpace(request.Version),
		}, j.Logf)
		if err != nil {
			return nil, err
		}
		j.Phase("已完成", "整合包导入完成。")
		return importJobResult{InstanceID: result.InstanceID, Name: result.Name}, nil
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, view)
}

// handlePackExport serves POST /api/packs/export: 202 EXPORT_PACK job.
func (a *API) handlePackExport(w http.ResponseWriter, r *http.Request) {
	var request packExportRequest
	if err := decodeJSON(w, r, &request); err != nil {
		a.writeError(w, r, err)
		return
	}
	instanceID := strings.TrimSpace(request.InstanceID)
	if instanceID == "" {
		a.writeError(w, r, apierr.Validation("instanceId 不能为空。"))
		return
	}
	mode := strings.TrimSpace(request.Mode)
	if mode != "profile" && mode != "home" {
		a.writeError(w, r, apierr.Validation("mode 只能是 profile 或 home。"))
		return
	}
	if _, ok := a.store.GetInstance(instanceID); !ok {
		a.writeError(w, r, notFoundError("实例不存在。"))
		return
	}
	view, err := a.jobs.Start(job.KindExportPack, 0, func(ctx context.Context, j *job.Job) (any, error) {
		j.Phase("导出实例", "正在打包实例 "+instanceID+"（"+mode+"）。")
		result, err := a.pack.Export(ctx, pack.ExportInput{InstanceID: instanceID, Mode: mode}, j.Logf)
		if err != nil {
			return nil, err
		}
		j.Phase("已完成", "实例导出完成。")
		return exportJobResult{ArtifactID: result.ArtifactID, Filename: result.Filename, Size: result.Size}, nil
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, view)
}

// writeUploadError writes an upload failure. A canceled request context means
// the client is gone: no response is attempted (the temporary file was already
// cleaned up by the upload helper).
func (a *API) writeUploadError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	a.writeError(w, r, err)
}
