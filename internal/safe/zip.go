// SPDX-License-Identifier: GPL-3.0-or-later

package safe

import (
	"archive/zip"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ZipArchive 是已通过容器与条目索引校验的 ZIP 归档。OpenZip 后应调用 Close 释放文件句柄。
type ZipArchive struct {
	path    string
	limits  Limits
	rc      *zip.ReadCloser
	entries []EntryInfo
	items   []zipItem
	closed  bool
}

// zipItem 保存解包所需的内部状态：原始 zip 文件、清洗后的路径、是否目录。
type zipItem struct {
	file  *zip.File
	clean string
	isDir bool
}

// OpenZip 打开并校验 ZIP 归档：要求普通文件、归档大小 <= MaxArchiveBytes，
// 条目数/元数据/路径/冲突/链接/特殊文件/加密/压缩方法全部校验通过；不落盘。
func OpenZip(path string, limits Limits) (*ZipArchive, error) {
	lim := normalizeLimits(limits)
	abs, info, err := statArchive(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > lim.MaxArchiveBytes {
		return nil, limitErrorf("归档文件大小 %d 超过上限 %d 字节: %q", info.Size(), lim.MaxArchiveBytes, filepath.Base(abs))
	}
	rc, err := zip.OpenReader(abs)
	if err != nil {
		return nil, corruptf("无法读取 ZIP 归档 %q: %v", filepath.Base(abs), err)
	}
	archive := &ZipArchive{path: abs, limits: lim, rc: rc}
	if err := archive.index(); err != nil {
		_ = rc.Close()
		return nil, err
	}
	return archive, nil
}

// index 遍历中央目录，校验容器与全部条目并保存清洗后的路径。
func (a *ZipArchive) index() error {
	validator := newIndexValidator(a.limits)
	a.entries = make([]EntryInfo, 0, len(a.rc.File))
	a.items = make([]zipItem, 0, len(a.rc.File))
	for _, file := range a.rc.File {
		if file.Flags&0x1 != 0 {
			return unsupportedf("ZIP 条目已加密（不支持）: %q", file.Name)
		}
		switch file.Method {
		case zip.Store, zip.Deflate:
		default:
			return unsupportedf("ZIP 条目使用了不支持的压缩方法 %d: %q", file.Method, file.Name)
		}
		isDir := file.FileInfo().IsDir() || strings.HasSuffix(file.Name, "/")
		compressed := file.CompressedSize64
		uncompressed := file.UncompressedSize64
		if compressed > uint64(maxInt64) || uncompressed > uint64(maxInt64) {
			return limitErrorf("ZIP 条目 %q 的大小超出可处理范围", file.Name)
		}
		entry := EntryInfo{
			Name:             file.Name,
			Size:             int64(compressed),
			UncompressedSize: int64(uncompressed),
			Mode:             file.Mode(),
			IsDir:            isDir,
		}
		if !isDir && uncompressed > uint64(compressionRatioThreshold) && compressed == 0 {
			return limitErrorf("ZIP 条目 %q 声明压缩大小为 0、解压大小 %d 字节，按压缩炸弹拒绝", file.Name, uncompressed)
		}
		clean, err := validator.add(entry)
		if err != nil {
			return err
		}
		a.entries = append(a.entries, entry)
		a.items = append(a.items, zipItem{file: file, clean: clean, isDir: isDir})
	}
	return nil
}

// Entries 返回条目信息拷贝（Name 为原始归档路径）。
func (a *ZipArchive) Entries() []EntryInfo {
	out := make([]EntryInfo, len(a.entries))
	copy(out, a.entries)
	return out
}

// Extract 将归档解包到 dest（必须不存在或为空目录）。逐条校验路径/父链/目标位置，
// 校验 CRC 与实际字节、压缩比、总量上限；支持 context 取消；失败时清理已写内容。
func (a *ZipArchive) Extract(ctx context.Context, dest string) error {
	if a == nil || a.rc == nil {
		return corruptf("ZIP 归档尚未打开")
	}
	if a.closed {
		return corruptf("ZIP 归档已关闭")
	}
	if err := checkCtx(ctx); err != nil {
		return err
	}
	guard, err := newDestGuard(dest)
	if err != nil {
		return err
	}
	total := int64(0)
	for _, item := range a.items {
		if err := checkCtx(ctx); err != nil {
			return guard.fail(err)
		}
		if item.isDir {
			if err := guard.makeDir(item.clean); err != nil {
				return guard.fail(err)
			}
			continue
		}
		reader, openErr := item.file.Open()
		if openErr != nil {
			return guard.fail(corruptf("读取 ZIP 条目 %q 失败: %v", item.clean, openErr))
		}
		crc := item.file.CRC32
		declared := int64(item.file.UncompressedSize64)
		writeErr := guard.writeFile(ctx, item.clean, reader, declared, &crc, a.limits, &total)
		closeErr := reader.Close()
		if writeErr != nil {
			return guard.fail(writeErr)
		}
		if closeErr != nil {
			return guard.fail(corruptf("关闭 ZIP 条目 %q 失败: %v", item.clean, closeErr))
		}
	}
	return nil
}

// Close 关闭归档文件。可重复调用。
func (a *ZipArchive) Close() error {
	if a == nil || a.rc == nil || a.closed {
		return nil
	}
	a.closed = true
	return a.rc.Close()
}

// statArchive 校验归档路径是已存在的普通文件（拒绝符号链接），返回绝对路径与信息。
func statArchive(path string) (string, fs.FileInfo, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil, corruptf("归档路径不能为空")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", nil, corruptf("解析归档路径失败: %v", err)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return "", nil, corruptf("无法访问归档文件 %q: %v", filepath.Base(abs), err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", nil, unsupportedf("归档不是普通文件: %q", filepath.Base(abs))
	}
	return abs, info, nil
}

// maxInt64 是 int64 最大值（避免依赖 math 包常量）。
const maxInt64 = int64(^uint64(0) >> 1)

// errExpandedLimit 表示解压流超过上限（由 boundedReader 返回）。
var errExpandedLimit = errors.New("解压流超过上限")
