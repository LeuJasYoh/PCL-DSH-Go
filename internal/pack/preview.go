// SPDX-License-Identifier: MIT

package pack

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Preview streams one uploaded archive to a private temp file, validates it and
// registers a 30-minute preview (docs/DSH_INTEGRATION.md §7.3). The archive is
// never installed, extracted or executed here.
func (s *Service) Preview(ctx context.Context, reader io.Reader, filename string) (Preview, error) {
	if reader == nil {
		return Preview{}, validationError("请上传整合包文件。")
	}
	if err := ctx.Err(); err != nil {
		return Preview{}, err
	}
	s.CleanupExpired(s.now())

	archivePath, archiveBytes, archiveSHA, err := s.saveUpload(ctx, reader)
	if err != nil {
		return Preview{}, err
	}
	inspection, err := inspectArchive(ctx, archivePath, filename, s.limits)
	if err != nil {
		_ = os.Remove(archivePath)
		return Preview{}, err
	}
	id, err := newPreviewID()
	if err != nil {
		_ = os.Remove(archivePath)
		return Preview{}, internalError("创建预览失败，请稍后重试。")
	}
	now := s.now()
	preview := Preview{
		ID:            id,
		Name:          inspection.name,
		Format:        inspection.format,
		Version:       inspection.version,
		DSHVersion:    inspection.dshVersion,
		Profiles:      append([]string(nil), inspection.profiles...),
		UnpackedBytes: inspection.unpackedBytes,
		Warnings:      append([]string(nil), inspection.warnings...),
		CreatedAt:     now,
		ExpiresAt:     now.Add(previewTTL),
	}
	s.mu.Lock()
	s.previews[id] = &previewRecord{
		preview:       preview,
		archivePath:   archivePath,
		archiveSHA256: archiveSHA,
		archiveBytes:  archiveBytes,
	}
	s.mu.Unlock()

	s.logger.Info("整合包预览完成",
		"previewId", id,
		"format", string(preview.Format),
		"name", preview.Name,
		"version", preview.Version,
		"dshVersion", preview.DSHVersion,
		"profiles", preview.Profiles,
		"unpackedBytes", preview.UnpackedBytes,
		"warnings", len(preview.Warnings),
	)
	return preview, nil
}

// CleanupExpired removes expired preview records and their uploaded archives
// (docs/M3_INTERFACES.md §3).
func (s *Service) CleanupExpired(now time.Time) {
	paths := make([]string, 0, 4)
	s.mu.Lock()
	for id, record := range s.previews {
		if now.Before(record.preview.ExpiresAt) {
			continue
		}
		delete(s.previews, id)
		if record.archivePath != "" {
			paths = append(paths, record.archivePath)
		}
	}
	s.mu.Unlock()
	for _, archivePath := range paths {
		_ = os.Remove(archivePath)
	}
}

// previewByID returns a live preview record or a 404 *apierr.Error when the id
// is malformed, unknown or expired. The archive file is removed lazily.
func (s *Service) previewByID(ctx context.Context, id string) (*previewRecord, error) {
	id = strings.TrimSpace(id)
	if !previewIDPattern.MatchString(id) {
		return nil, notFoundError("预览不存在或已过期，请重新上传。")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := s.now()
	s.mu.Lock()
	record, ok := s.previews[id]
	expired := ok && !now.Before(record.preview.ExpiresAt)
	if expired {
		delete(s.previews, id)
	}
	s.mu.Unlock()
	if expired {
		if record.archivePath != "" {
			_ = os.Remove(record.archivePath)
		}
		ok = false
	}
	if !ok {
		return nil, notFoundError("预览不存在或已过期，请重新上传。")
	}
	return record, nil
}

// saveUpload streams reader into <cache>/uploads with 0600 permissions while
// computing SHA-256 and enforcing the archive size limit.
func (s *Service) saveUpload(ctx context.Context, reader io.Reader) (path string, size int64, digest string, err error) {
	uploadsDir := s.uploadsDir()
	if mkdirErr := os.MkdirAll(uploadsDir, 0o700); mkdirErr != nil {
		return "", 0, "", internalError("保存上传文件失败，请检查数据目录权限。")
	}
	file, createErr := os.CreateTemp(uploadsDir, "upload-*.zip")
	if createErr != nil {
		return "", 0, "", internalError("保存上传文件失败，请检查数据目录权限。")
	}
	path = file.Name()
	committed := false
	defer func() {
		if !committed {
			_ = file.Close()
			_ = os.Remove(path)
		}
	}()
	if chmodErr := file.Chmod(0o600); chmodErr != nil {
		return "", 0, "", internalError("保存上传文件失败，请检查数据目录权限。")
	}

	hasher := sha256.New()
	buffer := make([]byte, uploadBufferBytes)
	var total int64
	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", 0, "", ctxErr
		}
		read, readErr := reader.Read(buffer)
		if read > 0 {
			total += int64(read)
			if total > s.limits.MaxArchiveBytes {
				return "", 0, "", tooLargeError(fmt.Sprintf("整合包超过 %d 字节上传上限。", s.limits.MaxArchiveBytes))
			}
			if _, writeErr := file.Write(buffer[:read]); writeErr != nil {
				return "", 0, "", internalError("保存上传文件失败，请检查数据目录权限。")
			}
			_, _ = hasher.Write(buffer[:read])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return "", 0, "", ctxErr
			}
			return "", 0, "", validationError("读取上传文件失败，请重试。")
		}
	}
	if total == 0 {
		return "", 0, "", validationError("上传的整合包为空。")
	}
	if syncErr := file.Sync(); syncErr != nil {
		return "", 0, "", internalError("保存上传文件失败，请检查数据目录权限。")
	}
	if closeErr := file.Close(); closeErr != nil {
		return "", 0, "", internalError("保存上传文件失败，请检查数据目录权限。")
	}
	committed = true
	return path, total, hex.EncodeToString(hasher.Sum(nil)), nil
}
