// SPDX-License-Identifier: GPL-3.0-or-later

package safe

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// TarArchive 是已通过 gzip/tar 扫描校验的 tar.gz 归档（不持有文件句柄）。
type TarArchive struct {
	path    string
	limits  Limits
	size    int64
	modTime int64
	entries []EntryInfo
	closed  bool
}

// tarEntry 是传给解包 visitor 的当前条目（reader 为 tar.Reader，已定位到条目数据）。
type tarEntry struct {
	info   EntryInfo
	clean  string
	reader io.Reader
}

// OpenTarGz 打开并校验 tar.gz 归档：普通文件、归档大小/条目数/路径/冲突/链接/特殊文件/
// 稀疏文件/PAX 元数据/展开上限/压缩率/尾随数据全部校验；不落盘。
func OpenTarGz(path string, limits Limits) (*TarArchive, error) {
	lim := normalizeLimits(limits)
	abs, info, err := statArchive(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > lim.MaxArchiveBytes {
		return nil, limitErrorf("归档文件大小 %d 超过上限 %d 字节: %q", info.Size(), lim.MaxArchiveBytes, filepath.Base(abs))
	}
	entries, err := scanTarGz(context.Background(), abs, info.Size(), lim, nil)
	if err != nil {
		return nil, err
	}
	return &TarArchive{path: abs, limits: lim, size: info.Size(), modTime: info.ModTime().UnixNano(), entries: entries}, nil
}

// Entries 返回条目信息拷贝（Name 为原始归档路径）。
func (a *TarArchive) Entries() []EntryInfo {
	out := make([]EntryInfo, len(a.entries))
	copy(out, a.entries)
	return out
}

// Extract 将归档解包到 dest（必须不存在或为空目录）。解包前复核归档文件未被替换，
// 扫描过程中重新执行全部安全校验并写盘；支持 context 取消；失败时清理已写内容。
func (a *TarArchive) Extract(ctx context.Context, dest string) error {
	if a == nil {
		return corruptf("tar.gz 归档尚未打开")
	}
	if a.closed {
		return corruptf("tar.gz 归档已关闭")
	}
	if err := checkCtx(ctx); err != nil {
		return err
	}
	info, err := os.Lstat(a.path)
	if err != nil {
		return corruptf("无法访问归档文件 %q: %v", filepath.Base(a.path), err)
	}
	if !info.Mode().IsRegular() || info.Size() != a.size || info.ModTime().UnixNano() != a.modTime {
		return corruptf("归档文件在打开后发生变化: %q", filepath.Base(a.path))
	}
	guard, err := newDestGuard(dest)
	if err != nil {
		return err
	}
	total := int64(0)
	visitor := func(entry tarEntry) error {
		if entry.info.IsDir {
			return guard.makeDir(entry.clean)
		}
		return guard.writeFile(ctx, entry.clean, entry.reader, entry.info.UncompressedSize, nil, a.limits, &total)
	}
	if _, err := scanTarGz(ctx, a.path, a.size, a.limits, visitor); err != nil {
		return guard.fail(err)
	}
	return nil
}

// Close 释放归档。可重复调用（tar.gz 扫描不保留句柄，本方法仅标记状态）。
func (a *TarArchive) Close() error {
	if a == nil {
		return nil
	}
	a.closed = true
	return nil
}

// scanTarGz 顺序扫描 tar.gz：校验所有安全规则；visitor 非 nil 时把当前条目交给调用方处理
// （调用方必须完整读取条目数据），为 nil 时丢弃条目数据。返回按归档顺序的条目信息。
func scanTarGz(ctx context.Context, path string, archiveSize int64, limits Limits, visitor func(tarEntry) error) ([]EntryInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, corruptf("打开归档文件失败: %v", err)
	}
	defer func() { _ = file.Close() }()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return nil, corruptf("gzip 解压失败: %v", err)
	}
	defer func() { _ = gzipReader.Close() }()

	bounded := &boundedReader{reader: gzipReader, limit: expandedCap(limits)}
	tarReader := tar.NewReader(bounded)
	validator := newIndexValidator(limits)
	entries := make([]EntryInfo, 0, 64)

	for {
		if err := checkCtx(ctx); err != nil {
			return nil, err
		}
		header, nextErr := tarReader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			if errors.Is(nextErr, errExpandedLimit) {
				return nil, limitErrorf("tar.gz 解压流超过上限: %v", nextErr)
			}
			if ctx != nil && ctx.Err() != nil {
				return nil, cancelledError(ctx.Err())
			}
			return nil, corruptf("tar 头解析失败（归档损坏或元数据异常）: %v", nextErr)
		}
		if header.Typeflag == tar.TypeGNUSparse || hasGUNSparseRecords(header) {
			return nil, unsupportedf("tar 条目是稀疏文件（不支持）: %q", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeReg, tar.TypeRegA, tar.TypeDir:
		default:
			return nil, unsupportedf("tar 条目类型 %q 不支持（仅允许普通文件与目录）: %q", string(header.Typeflag), header.Name)
		}
		isDir := header.Typeflag == tar.TypeDir
		entry := EntryInfo{
			Name:             header.Name,
			Size:             0, // tar 不提供单条目压缩大小
			UncompressedSize: header.Size,
			Mode:             header.FileInfo().Mode(),
			IsDir:            isDir,
		}
		clean, addErr := validator.add(entry)
		if addErr != nil {
			return nil, addErr
		}
		entries = append(entries, entry)

		if validator.Total() > compressionRatioThreshold {
			ratio := validator.Total() / maxInt64Value(1, archiveSize)
			if ratio > limits.MaxCompressionRatio {
				return nil, limitErrorf("tar.gz 压缩率超过上限 %d（解压后 %d 字节 / 归档 %d 字节）", limits.MaxCompressionRatio, validator.Total(), archiveSize)
			}
		}

		if visitor != nil {
			if visitErr := visitor(tarEntry{info: entry, clean: clean, reader: tarReader}); visitErr != nil {
				return nil, visitErr
			}
			continue
		}
		if err := consumeTarEntry(ctx, tarReader, header.Size); err != nil {
			return nil, err
		}
	}

	if err := checkTrailingData(ctx, bounded); err != nil {
		return nil, err
	}
	return entries, nil
}

// consumeTarEntry 完整读取当前条目数据，用于校验阶段推进流位置。
func consumeTarEntry(ctx context.Context, reader io.Reader, size int64) error {
	buffer := make([]byte, 64*1024)
	remaining := size
	for remaining > 0 {
		if err := checkCtx(ctx); err != nil {
			return err
		}
		chunk := int64(len(buffer))
		if chunk > remaining {
			chunk = remaining
		}
		n, err := io.ReadFull(reader, buffer[:chunk])
		remaining -= int64(n)
		if err != nil {
			if errors.Is(err, errExpandedLimit) {
				return limitErrorf("tar.gz 解压流超过上限")
			}
			if ctx != nil && ctx.Err() != nil {
				return cancelledError(ctx.Err())
			}
			return corruptf("tar 条目数据截断: %v", err)
		}
	}
	return nil
}

// checkTrailingData 校验 tar 结束标记之后只有零填充（允许少量 GNU tar 记录填充）。
func checkTrailingData(ctx context.Context, reader io.Reader) error {
	const maxTrailingBytes = 1 << 20
	buffer := make([]byte, 32*1024)
	var read int64
	for {
		if err := checkCtx(ctx); err != nil {
			return err
		}
		n, err := reader.Read(buffer)
		for index := 0; index < n; index++ {
			if buffer[index] != 0 {
				return corruptf("tar 结束标记后含额外数据")
			}
		}
		read += int64(n)
		if read > maxTrailingBytes {
			return corruptf("tar 尾随数据超过 %d 字节", maxTrailingBytes)
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			if errors.Is(err, errExpandedLimit) {
				return limitErrorf("tar.gz 解压流超过上限: %v", err)
			}
			return corruptf("读取 tar 尾随数据失败: %v", err)
		}
	}
}

// hasGUNSparseRecords 判断 PAX 记录中是否包含 GNU 稀疏文件元数据。
func hasGUNSparseRecords(header *tar.Header) bool {
	for key := range header.PAXRecords {
		if strings.HasPrefix(key, "GNU.sparse.") {
			return true
		}
	}
	return false
}

// boundedReader 限制解压流总字节数，防止 gzip 炸弹绕过声明大小。
type boundedReader struct {
	reader io.Reader
	total  int64
	limit  int64
}

// Read 实现 io.Reader。
func (b *boundedReader) Read(buffer []byte) (int, error) {
	n, err := b.reader.Read(buffer)
	b.total += int64(n)
	if b.total > b.limit {
		return n, errExpandedLimit
	}
	return n, err
}

// expandedCap 计算解压流兜底上限：解压总量上限 + 允许的 tar 头/填充开销。
func expandedCap(limits Limits) int64 {
	const perEntryOverhead = 1536 // 512 header + 512 padding + 512 余量
	overhead := int64(limits.MaxEntries) * perEntryOverhead
	if overhead < 0 || overhead > maxInt64/4 {
		return maxInt64
	}
	if limits.MaxUnpackedBytes > maxInt64-overhead-(1<<20) {
		return maxInt64
	}
	return limits.MaxUnpackedBytes + overhead + (1 << 20)
}

// maxInt64Value 返回 a、b 中较大者（b 至少为 1 的调用方已保证）。
func maxInt64Value(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
