// SPDX-License-Identifier: MIT

package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"strings"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
)

const (
	// maxPackUploadBytes is the documented multipart upload limit for pack
	// previews (docs/IMPLEMENTATION_SPEC.md 4.1: 2 GiB).
	maxPackUploadBytes int64 = 2 << 30
	// maxMultipartOverheadBytes covers boundaries, part headers and any
	// non-file form fields around the 2 GiB payload.
	maxMultipartOverheadBytes int64 = 8 << 20
	// maxUploadFilenameRunes bounds the display-only filename.
	maxUploadFilenameRunes = 200
	// defaultUploadFilename is used when the client sends no usable name.
	defaultUploadFilename = "upload.pack"
)

// uploadedFile is a streamed multipart upload that has been written to disk.
// Path is a private temporary file (0600); the caller owns it and must remove
// it once pack.Preview has consumed the content. Filename is the untrusted
// client-provided name sanitized for display only.
type uploadedFile struct {
	Path     string
	Filename string
	Size     int64
}

// receiveUpload streams the "file" field of a multipart/form-data request into a
// fresh 0600 file under dir. It never buffers the payload in memory, rejects
// bodies larger than limit with 413 PAYLOAD_TOO_LARGE, rejects missing or
// duplicate file fields with 400 BAD_REQUEST, and removes the temporary file on
// every failure (including context cancellation).
func receiveUpload(ctx context.Context, r *http.Request, dir string, limit int64) (uploadedFile, error) {
	if r == nil || r.Body == nil {
		return uploadedFile{}, apierr.New(http.StatusBadRequest, apierr.CodeBadRequest, "请求体不能为空。")
	}
	if limit <= 0 {
		limit = maxPackUploadBytes
	}
	boundary, err := multipartBoundary(r.Header.Get("Content-Type"))
	if err != nil {
		return uploadedFile{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return uploadedFile{}, fmt.Errorf("创建上传目录失败: %w", err)
	}

	totalCap := limit + maxMultipartOverheadBytes
	// The counter bounds the whole request body (including ignored form fields),
	// so a malicious part cannot force unbounded reads.
	counter := &countingReader{reader: io.LimitReader(r.Body, totalCap+1)}
	reader := multipart.NewReader(counter, boundary)

	var (
		filePath string
		filename string
		size     int64
		file     *os.File
		haveFile bool
	)
	cleanup := func() {
		if file != nil {
			_ = file.Close()
			file = nil
		}
		if filePath != "" {
			_ = os.Remove(filePath)
			filePath = ""
		}
	}
	tooLarge := func() error {
		return apierr.New(http.StatusRequestEntityTooLarge, apierr.CodePayloadTooLarge,
			"上传文件超过大小上限。")
	}

	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			cleanup()
			return uploadedFile{}, ctxErr
		}
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			cleanup()
			if ctxErr := ctx.Err(); ctxErr != nil {
				return uploadedFile{}, ctxErr
			}
			if counter.n > totalCap {
				return uploadedFile{}, tooLarge()
			}
			return uploadedFile{}, apierr.New(http.StatusBadRequest, apierr.CodeBadRequest,
				"multipart 表单解析失败。")
		}
		if part.FormName() != "file" {
			// Non-file fields are ignored, but their bytes still count against
			// the request cap (and cancellation is honoured while draining).
			_, drainErr := io.Copy(io.Discard, ctxReader{ctx: ctx, reader: part})
			_ = part.Close()
			if drainErr != nil {
				cleanup()
				if ctxErr := ctx.Err(); ctxErr != nil {
					return uploadedFile{}, ctxErr
				}
				if counter.n > totalCap {
					return uploadedFile{}, tooLarge()
				}
				return uploadedFile{}, fmt.Errorf("读取 multipart 表单失败: %w", drainErr)
			}
			continue
		}
		if haveFile || file != nil {
			_ = part.Close()
			cleanup()
			return uploadedFile{}, apierr.New(http.StatusBadRequest, apierr.CodeBadRequest,
				"一次只能上传一个文件。")
		}
		rawName := part.FileName()
		if rawName == "" {
			_ = part.Close()
			cleanup()
			return uploadedFile{}, apierr.New(http.StatusBadRequest, apierr.CodeBadRequest,
				"缺少 file 文件字段。")
		}
		created, createErr := os.CreateTemp(dir, "upload-*.pack")
		if createErr != nil {
			_ = part.Close()
			cleanup()
			return uploadedFile{}, fmt.Errorf("创建上传临时文件失败: %w", createErr)
		}
		file = created
		filePath = created.Name()
		filename = sanitizeUploadFilename(rawName)

		copied, copyErr := io.Copy(created, ctxReader{ctx: ctx, reader: io.LimitReader(part, limit+1)})
		_ = part.Close()
		if copyErr != nil {
			cleanup()
			if ctxErr := ctx.Err(); ctxErr != nil {
				return uploadedFile{}, ctxErr
			}
			if counter.n > totalCap {
				return uploadedFile{}, tooLarge()
			}
			return uploadedFile{}, fmt.Errorf("读取上传文件失败: %w", copyErr)
		}
		if copied > limit {
			cleanup()
			return uploadedFile{}, tooLarge()
		}
		size = copied
		if closeErr := created.Close(); closeErr != nil {
			file = nil
			cleanup()
			return uploadedFile{}, fmt.Errorf("写入上传临时文件失败: %w", closeErr)
		}
		file = nil
		haveFile = true
	}

	if !haveFile {
		cleanup()
		return uploadedFile{}, apierr.New(http.StatusBadRequest, apierr.CodeBadRequest,
			"缺少 file 文件字段。")
	}
	if counter.n > totalCap {
		cleanup()
		return uploadedFile{}, tooLarge()
	}
	return uploadedFile{Path: filePath, Filename: filename, Size: size}, nil
}

// multipartBoundary extracts the multipart boundary from a Content-Type header.
func multipartBoundary(contentType string) (string, error) {
	invalid := func() error {
		return apierr.New(http.StatusBadRequest, apierr.CodeBadRequest,
			"请求必须是 multipart/form-data 格式。")
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		return "", invalid()
	}
	boundary := params["boundary"]
	if boundary == "" {
		return "", invalid()
	}
	return boundary, nil
}

// sanitizeUploadFilename turns an untrusted multipart filename into a safe
// display value: no path components, no control characters, bounded length.
func sanitizeUploadFilename(raw string) string {
	name := raw
	if index := strings.LastIndexAny(name, "/\\"); index >= 0 {
		name = name[index+1:]
	}
	var builder strings.Builder
	for _, char := range name {
		if char < 0x20 || char == 0x7f {
			continue
		}
		builder.WriteRune(char)
	}
	name = strings.TrimSpace(builder.String())
	if name == "" {
		return defaultUploadFilename
	}
	runes := []rune(name)
	if len(runes) > maxUploadFilenameRunes {
		name = string(runes[:maxUploadFilenameRunes])
	}
	return name
}

// countingReader counts the bytes read from the wrapped reader.
type countingReader struct {
	reader io.Reader
	n      int64
}

// Read implements io.Reader.
func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	c.n += int64(n)
	return n, err
}

// ctxReader aborts a stream as soon as the context is cancelled.
type ctxReader struct {
	ctx    context.Context
	reader io.Reader
}

// Read implements io.Reader.
func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.reader.Read(p)
}
