// SPDX-License-Identifier: GPL-3.0-or-later

// Package safe 是实现整合包安全归档的核心：ZIP/TAR 限值与路径规则、条目冲突检测、
// 链接/特殊文件拒绝、CRC 与压缩比校验、安全的解包（父链无符号链接、目标不越界、
// 失败清理、context 取消）。
//
// 契约见 docs/M3_INTERFACES.md 第 2 节与 docs/DSH_INTEGRATION.md 7.2。
// 本包从不执行归档内的任何内容。
package safe

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
)

const (
	// 默认限值（docs/DSH_INTEGRATION.md 7.2）。
	defaultMaxArchiveBytes     int64 = 2 << 30 // 2 GiB
	defaultMaxUnpackedBytes    int64 = 6 << 30 // 6 GiB
	defaultMaxFileBytes        int64 = 2 << 30 // 2 GiB
	defaultMaxMetadataBytes    int64 = 4 << 20 // 4 MiB
	defaultMaxEntries                = 400000
	defaultMaxPathLength             = 1000
	defaultMaxCompressionRatio int64 = 1000
	compressionRatioThreshold  int64 = 1 << 20 // 仅当解压后 >1 MiB 时判定压缩率
)

// Limits 是归档安全校验的各项上限。任一字段 <=0 时使用 DefaultLimits 的对应默认值。
type Limits struct {
	MaxArchiveBytes     int64 // 归档文件大小上限（压缩后）
	MaxUnpackedBytes    int64 // 全部条目解压后总大小上限
	MaxFileBytes        int64 // 单条文件解压后大小上限
	MaxMetadataBytes    int64 // 元数据文件（manifest/package.json 等）大小上限
	MaxEntries          int   // 条目数上限
	MaxPathLength       int   // 归档路径字节长度上限
	MaxCompressionRatio int64 // 压缩率上限（解压后 >1 MiB 时判定）
}

// DefaultLimits 返回冻结文档中的默认限值。
func DefaultLimits() Limits {
	return Limits{
		MaxArchiveBytes:     defaultMaxArchiveBytes,
		MaxUnpackedBytes:    defaultMaxUnpackedBytes,
		MaxFileBytes:        defaultMaxFileBytes,
		MaxMetadataBytes:    defaultMaxMetadataBytes,
		MaxEntries:          defaultMaxEntries,
		MaxPathLength:       defaultMaxPathLength,
		MaxCompressionRatio: defaultMaxCompressionRatio,
	}
}

// normalizeLimits 把 <=0 的字段补成默认值；调用方传入零值 Limits 时等价于默认限值。
func normalizeLimits(limits Limits) Limits {
	out := limits
	defaults := DefaultLimits()
	if out.MaxArchiveBytes <= 0 {
		out.MaxArchiveBytes = defaults.MaxArchiveBytes
	}
	if out.MaxUnpackedBytes <= 0 {
		out.MaxUnpackedBytes = defaults.MaxUnpackedBytes
	}
	if out.MaxFileBytes <= 0 {
		out.MaxFileBytes = defaults.MaxFileBytes
	}
	if out.MaxMetadataBytes <= 0 {
		out.MaxMetadataBytes = defaults.MaxMetadataBytes
	}
	if out.MaxEntries <= 0 {
		out.MaxEntries = defaults.MaxEntries
	}
	if out.MaxPathLength <= 0 {
		out.MaxPathLength = defaults.MaxPathLength
	}
	if out.MaxCompressionRatio <= 0 {
		out.MaxCompressionRatio = defaults.MaxCompressionRatio
	}
	return out
}

// EntryInfo 描述归档中的一个条目；Name 为原始归档路径（未清洗，目录可能带结尾斜杠）。
type EntryInfo struct {
	Name             string
	Size             int64 // 压缩后大小（TAR 不提供单条目压缩大小，为 0）
	UncompressedSize int64
	Mode             fs.FileMode
	IsDir            bool
}

// ErrKind 是安全归档错误的分类，供上层映射为 HTTP 400/422 等。
type ErrKind string

const (
	// ErrUnsafePath 表示路径/目标目录不安全（穿越、绝对路径、保留名、符号链接父链等）。
	ErrUnsafePath ErrKind = "unsafe_path"
	// ErrEntryConflict 表示条目路径重复、大小写/NFC 冲突或文件与目录冲突。
	ErrEntryConflict ErrKind = "entry_conflict"
	// ErrLimitExceeded 表示超过任何一项限值。
	ErrLimitExceeded ErrKind = "limit_exceeded"
	// ErrUnsupported 表示不支持的压缩/加密、链接、特殊文件或稀疏文件。
	ErrUnsupported ErrKind = "unsupported"
	// ErrCorrupt 表示归档损坏、CRC/大小不一致、截断或 I/O 失败。
	ErrCorrupt ErrKind = "corrupt"
	// ErrCancelled 表示操作被 context 取消。
	ErrCancelled ErrKind = "cancelled"
)

// Error 是安全归档错误；Kind 便于上层分类处理，Message 为中文可操作提示。
type Error struct {
	Kind    ErrKind
	Message string
}

// Error 实现 error。
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return string(e.Kind) + ": " + e.Message
}

// KindOf 返回错误的安全归档分类；非本包错误返回空字符串。
func KindOf(err error) ErrKind {
	var safeErr *Error
	if errors.As(err, &safeErr) {
		return safeErr.Kind
	}
	return ""
}

func newError(kind ErrKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

func unsafePathf(format string, args ...any) *Error {
	return newError(ErrUnsafePath, format, args...)
}

func conflictf(format string, args ...any) *Error {
	return newError(ErrEntryConflict, format, args...)
}

func limitErrorf(format string, args ...any) *Error {
	return newError(ErrLimitExceeded, format, args...)
}

func unsupportedf(format string, args ...any) *Error {
	return newError(ErrUnsupported, format, args...)
}

func corruptf(format string, args ...any) *Error {
	return newError(ErrCorrupt, format, args...)
}

func cancelledError(cause error) *Error {
	message := "归档操作已取消"
	if cause != nil {
		message += ": " + cause.Error()
	}
	return &Error{Kind: ErrCancelled, Message: message}
}

// checkCtx 在解包/扫描过程中检查取消；ctx 为 nil 时视为不取消。
func checkCtx(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return cancelledError(err)
	}
	return nil
}

// ValidateEntries 校验一组条目（不读归档内容），供已解压 staging 复验或测试使用。
// 校验规则与 OpenZip/OpenTarGz 的索引阶段一致：路径、冲突、链接/特殊文件、大小与条目数限值、
// 元数据大小、压缩率（仅当 EntryInfo.Size > 0 时判定）。
func ValidateEntries(entries []EntryInfo, limits Limits) error {
	lim := normalizeLimits(limits)
	validator := newIndexValidator(lim)
	for _, entry := range entries {
		if _, err := validator.add(entry); err != nil {
			return err
		}
	}
	return nil
}
