// SPDX-License-Identifier: GPL-3.0-or-later

package safe

import (
	"context"
	"errors"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// destGuard 管理解包目标目录：创建/校验、条目落盘、失败清理。
type destGuard struct {
	path    string
	existed bool
}

// newDestGuard 校验 dest（必须不存在或为空目录），并在需要时创建。
// 目标父链（直到文件系统根）不得包含符号链接。
func newDestGuard(dest string) (*destGuard, error) {
	if strings.TrimSpace(dest) == "" {
		return nil, unsafePathf("解包目标目录不能为空")
	}
	abs, err := filepath.Abs(dest)
	if err != nil {
		return nil, unsafePathf("解析解包目标目录失败: %v", err)
	}
	abs = filepath.Clean(abs)
	if err := ensureNoSymlinkAncestors(filepath.Dir(abs)); err != nil {
		return nil, err
	}
	info, statErr := os.Lstat(abs)
	switch {
	case statErr == nil:
		if info.Mode()&fs.ModeSymlink != 0 {
			return nil, unsafePathf("解包目标不能是符号链接: %q", abs)
		}
		if !info.IsDir() {
			return nil, unsafePathf("解包目标已存在但不是目录: %q", abs)
		}
		entries, err := os.ReadDir(abs)
		if err != nil {
			return nil, corruptf("读取解包目标目录失败: %v", err)
		}
		if len(entries) > 0 {
			return nil, unsafePathf("解包目标必须是不存在的目录或空目录: %q", abs)
		}
		return &destGuard{path: abs, existed: true}, nil
	case errors.Is(statErr, fs.ErrNotExist):
		if err := os.MkdirAll(abs, 0o700); err != nil {
			return nil, corruptf("创建解包目标目录失败: %v", err)
		}
		return &destGuard{path: abs}, nil
	default:
		return nil, corruptf("检查解包目标目录失败: %v", statErr)
	}
}

// ensureNoSymlinkAncestors 逐级检查已存在的父目录，拒绝符号链接与非目录。
func ensureNoSymlinkAncestors(dir string) error {
	current := filepath.Clean(dir)
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&fs.ModeSymlink != 0 {
				return unsafePathf("解包目标父链包含符号链接: %q", current)
			}
			if !info.IsDir() {
				return unsafePathf("解包目标父链包含非目录路径: %q", current)
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return corruptf("检查解包目标父链失败: %v", err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

// fail 在失败时清理解包目标，再返回原始错误（若清理也失败则附加说明）。
func (g *destGuard) fail(err error) error {
	if cleanupErr := g.cleanup(); cleanupErr != nil {
		var safeErr *Error
		if errors.As(err, &safeErr) {
			return &Error{Kind: safeErr.Kind, Message: safeErr.Message + "；清理解包目标失败: " + cleanupErr.Error()}
		}
		return corruptf("%v；清理解包目标失败: %v", err, cleanupErr)
	}
	return err
}

// cleanup 删除本次解包写入的内容：目标由本包创建时整体删除，否则清空目录内容。
func (g *destGuard) cleanup() error {
	if g.path == "" {
		return nil
	}
	if !g.existed {
		return os.RemoveAll(g.path)
	}
	entries, err := os.ReadDir(g.path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(g.path, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

// target 把清洗后的相对路径解析到解包目标内，并再次确认不会越界。
func (g *destGuard) target(cleanName string) (string, error) {
	target := filepath.Join(g.path, filepath.FromSlash(cleanName))
	rel, err := filepath.Rel(g.path, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", unsafePathf("归档条目路径越出解包目标: %q", cleanName)
	}
	return target, nil
}

// makeDir 创建归档中的目录条目。
func (g *destGuard) makeDir(cleanName string) error {
	target, err := g.target(cleanName)
	if err != nil {
		return err
	}
	info, statErr := os.Lstat(target)
	switch {
	case statErr == nil:
		if info.Mode()&fs.ModeSymlink != 0 {
			return unsupportedf("解包目标路径是符号链接: %q", cleanName)
		}
		if !info.IsDir() {
			return conflictf("归档内文件与目录冲突: %q", cleanName)
		}
		return nil
	case errors.Is(statErr, fs.ErrNotExist):
		if err := os.MkdirAll(target, 0o755); err != nil {
			return corruptf("创建解包目录 %q 失败: %v", cleanName, err)
		}
		return nil
	default:
		return corruptf("检查解包目录 %q 失败: %v", cleanName, statErr)
	}
}

// writeFile 把当前归档条目写到解包目标内：校验实际字节/CRC/总量/单文件上限，
// 每 64 KiB 检查一次 context 取消。
func (g *destGuard) writeFile(ctx context.Context, cleanName string, src io.Reader, declared int64, expectedCRC *uint32, limits Limits, total *int64) error {
	target, err := g.target(cleanName)
	if err != nil {
		return err
	}
	if info, statErr := os.Lstat(target); statErr == nil {
		if info.Mode()&fs.ModeSymlink != 0 {
			return unsupportedf("解包目标路径是符号链接: %q", cleanName)
		}
		return conflictf("解包目标已存在: %q", cleanName)
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return corruptf("检查解包目标 %q 失败: %v", cleanName, statErr)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return corruptf("创建解包父目录失败: %v", err)
	}
	file, openErr := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if openErr != nil {
		return corruptf("创建解包文件 %q 失败: %v", cleanName, openErr)
	}

	buffer := make([]byte, 64*1024)
	var written int64
	hash := crc32.NewIEEE()
	for {
		if err := checkCtx(ctx); err != nil {
			_ = file.Close()
			return err
		}
		n, readErr := src.Read(buffer)
		if n > 0 {
			written += int64(n)
			if written > limits.MaxFileBytes {
				_ = file.Close()
				return limitErrorf("文件 %q 实际解压字节超过单文件上限 %d", cleanName, limits.MaxFileBytes)
			}
			if declared >= 0 && written > declared {
				_ = file.Close()
				return corruptf("文件 %q 实际字节超过声明大小 %d", cleanName, declared)
			}
			*total += int64(n)
			if *total > limits.MaxUnpackedBytes {
				_ = file.Close()
				return limitErrorf("归档解压总量超过上限 %d 字节", limits.MaxUnpackedBytes)
			}
			hash.Write(buffer[:n])
			if _, err := file.Write(buffer[:n]); err != nil {
				_ = file.Close()
				return corruptf("写入解包文件 %q 失败: %v", cleanName, err)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = file.Close()
			if errors.Is(readErr, errExpandedLimit) {
				return limitErrorf("解压流超过上限: %v", readErr)
			}
			if ctx != nil && ctx.Err() != nil {
				return cancelledError(ctx.Err())
			}
			return corruptf("读取归档条目 %q 失败: %v", cleanName, readErr)
		}
	}
	if err := file.Close(); err != nil {
		return corruptf("关闭解包文件 %q 失败: %v", cleanName, err)
	}
	if declared >= 0 && written != declared {
		return corruptf("文件 %q 大小与声明不一致（声明 %d，实际 %d）", cleanName, declared, written)
	}
	if expectedCRC != nil && hash.Sum32() != *expectedCRC {
		return corruptf("文件 %q CRC 校验失败", cleanName)
	}
	return nil
}
