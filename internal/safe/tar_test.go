// SPDX-License-Identifier: GPL-3.0-or-later

package safe

import (
	"archive/tar"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenTarGzValidArchiveAndExtract(t *testing.T) {
	tarPath := buildTarGz(t, []tarSample{
		{name: "dspack.json", data: []byte("{\"format\":\"dspack\",\"version\":3}")},
		{name: "overrides/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "overrides/skills/demo/SKILL.md", data: []byte("# safe skill")},
		{name: "home/中文/文件.txt", data: []byte("内容")},
		{name: strings.Repeat("dir/", 30) + "deep.txt", data: []byte("long path")},
	})
	archive, err := OpenTarGz(tarPath, Limits{})
	if err != nil {
		t.Fatalf("OpenTarGz 合法归档失败: %v", err)
	}
	defer func() {
		if err := archive.Close(); err != nil {
			t.Fatalf("Close 失败: %v", err)
		}
	}()
	entries := archive.Entries()
	if len(entries) != 5 {
		t.Fatalf("条目数 = %d，期望 5", len(entries))
	}
	if entries[0].Name != "dspack.json" {
		t.Fatalf("首条目 = %q", entries[0].Name)
	}
	dest := filepath.Join(t.TempDir(), "out")
	if err := archive.Extract(context.Background(), dest); err != nil {
		t.Fatalf("Extract 失败: %v", err)
	}
	for relative, want := range map[string]string{
		"overrides/skills/demo/SKILL.md": "# safe skill",
		"home/中文/文件.txt":                 "内容",
	} {
		data, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", relative, err)
		}
		if string(data) != want {
			t.Fatalf("%s 内容 = %q，期望 %q", relative, string(data), want)
		}
	}
}

func TestOpenTarGzRejectsUnsafePaths(t *testing.T) {
	cases := []struct {
		name   string
		sample tarSample
		limits Limits
	}{
		{"穿越路径", tarSample{name: "overrides/../../escape", data: []byte("x")}, Limits{}},
		{"绝对路径", tarSample{name: "/etc/passwd", data: []byte("x")}, Limits{}},
		{"盘符路径", tarSample{name: "C:/evil", data: []byte("x")}, Limits{}},
		{"反斜杠路径", tarSample{name: "overrides\\bad.txt", data: []byte("x")}, Limits{}},
		{"Windows 保留名", tarSample{name: "overrides/NUL.txt", data: []byte("x")}, Limits{}},
		{"尾随点", tarSample{name: "overrides/bad.", data: []byte("x")}, Limits{}},
		{"尾随空格", tarSample{name: "overrides/bad ", data: []byte("x")}, Limits{}},
		{"冒号", tarSample{name: "overrides/a:b", data: []byte("x")}, Limits{}},
		{"点段", tarSample{name: "overrides/./x", data: []byte("x")}, Limits{}},
		{"空段", tarSample{name: "overrides//x", data: []byte("x")}, Limits{}},
		{"控制字符", tarSample{name: "overrides/\x01bad", data: []byte("x")}, Limits{}},
		{"组合字符", tarSample{name: "cafe\u0301.txt", data: []byte("x")}, Limits{}},
		{"超长路径", tarSample{name: strings.Repeat("a", 40), data: []byte("x")}, Limits{MaxPathLength: 10}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			tarPath := buildTarGz(t, []tarSample{testCase.sample})
			archive, err := OpenTarGz(tarPath, testCase.limits)
			if archive != nil {
				_ = archive.Close()
			}
			assertKind(t, err, ErrUnsafePath, ErrLimitExceeded)
		})
	}
}

func TestOpenTarGzRejectsConflicts(t *testing.T) {
	cases := []struct {
		name    string
		samples []tarSample
	}{
		{"重复路径", []tarSample{{name: "a/b", data: []byte("1")}, {name: "a/b", data: []byte("2")}}},
		{"大小写冲突", []tarSample{{name: "A/x", data: []byte("1")}, {name: "a/y", data: []byte("2")}}},
		{"文件目录冲突", []tarSample{{name: "a/data", data: []byte("1")}, {name: "a/data/file", data: []byte("2")}}},
		{"目录文件冲突", []tarSample{{name: "a", typeflag: tar.TypeDir}, {name: "a", data: []byte("1")}}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			tarPath := buildTarGz(t, testCase.samples)
			archive, err := OpenTarGz(tarPath, Limits{})
			if archive != nil {
				_ = archive.Close()
			}
			assertKind(t, err, ErrEntryConflict)
		})
	}
}

// TestOpenTarGzRejectsLinksAndSpecialFiles 覆盖符号链接/硬链接/设备/管道/未知类型。
func TestOpenTarGzRejectsLinksAndSpecialFiles(t *testing.T) {
	cases := []struct {
		name     string
		typeflag byte
		linkname string
	}{
		{"符号链接", tar.TypeSymlink, "../../outside"},
		{"硬链接", tar.TypeLink, "target"},
		{"字符设备", tar.TypeChar, ""},
		{"块设备", tar.TypeBlock, ""},
		{"命名管道", tar.TypeFifo, ""},
		{"未知类型", 'Z', ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			tarPath := buildRawTarGz(t, rawTarHeader("entry", 0, testCase.typeflag, testCase.linkname), zeroBlocks(2))
			archive, err := OpenTarGz(tarPath, Limits{})
			if archive != nil {
				_ = archive.Close()
			}
			assertKind(t, err, ErrUnsupported, ErrCorrupt)
		})
	}
}

func TestOpenTarGzRejectsSparseAndPaxMetadata(t *testing.T) {
	// GNU 旧式稀疏文件头（typeflag 'S'）。
	sparsePath := buildRawTarGz(t, rawTarHeader("sparse", 0, tar.TypeGNUSparse, ""), zeroBlocks(2))
	archive, err := OpenTarGz(sparsePath, Limits{})
	if archive != nil {
		_ = archive.Close()
	}
	assertKind(t, err, ErrUnsupported, ErrCorrupt)

	// 超大 PAX 元数据头（5 MiB > 标准库 1 MiB 元数据上限）。
	hugePax := buildRawTarGz(t,
		rawTarHeader("./PaxHeaders/x", 5<<20, tar.TypeXHeader, ""),
		make([]byte, 5<<20),
		zeroBlocks(2),
	)
	archive, err = OpenTarGz(hugePax, Limits{})
	if archive != nil {
		_ = archive.Close()
	}
	assertKind(t, err, ErrCorrupt, ErrUnsupported, ErrLimitExceeded)
}

func TestOpenTarGzRejectsCorruptHeadersAndTruncation(t *testing.T) {
	badChecksum := rawTarHeader("bad-checksum", 0, tar.TypeReg, "")
	badChecksum[0] = 'X' // 修改名称但不更新校验和
	checksumPath := buildRawTarGz(t, badChecksum, zeroBlocks(2))

	truncatedData := buildRawTarGz(t,
		rawTarHeader("truncated", 100, tar.TypeReg, ""),
		[]byte("0123456789"),
	)

	validBytes, err := os.ReadFile(buildTarGz(t, []tarSample{{name: "a.txt", data: []byte("hello")}}))
	if err != nil {
		t.Fatalf("读取 tar.gz 失败: %v", err)
	}
	truncatedGzip := writeTestFile(t, t.TempDir(), "truncated.tar.gz", validBytes[:len(validBytes)/2])
	garbage := writeTestFile(t, t.TempDir(), "garbage.tar.gz", []byte("this is not gzip"))

	for name, path := range map[string]string{
		"校验和错误":   checksumPath,
		"数据截断":    truncatedData,
		"gzip 截断": truncatedGzip,
		"gzip 垃圾": garbage,
	} {
		t.Run(name, func(t *testing.T) {
			archive, err := OpenTarGz(path, Limits{})
			if archive != nil {
				_ = archive.Close()
			}
			assertKind(t, err, ErrCorrupt, ErrUnsupported)
		})
	}
}

func TestOpenTarGzTrailingDataRules(t *testing.T) {
	validTar := buildTarBytes(t, []tarSample{{name: "a.txt", data: []byte("hello")}})
	withJunk := buildRawTarGz(t, validTar, []byte("JUNK-AFTER-EOF"))
	junkArchive, junkErr := OpenTarGz(withJunk, Limits{})
	if junkArchive != nil {
		_ = junkArchive.Close()
	}
	assertKind(t, junkErr, ErrCorrupt)

	withZeros := buildRawTarGz(t, validTar, zeroBlocks(8))
	archive, err := OpenTarGz(withZeros, Limits{})
	if err != nil {
		t.Fatalf("尾随零填充应被允许: %v", err)
	}
	_ = archive.Close()

	// gzip 多成员/尾随垃圾。
	validGz, err := os.ReadFile(buildTarGz(t, []tarSample{{name: "a.txt", data: []byte("hello")}}))
	if err != nil {
		t.Fatalf("读取 gzip 失败: %v", err)
	}
	appended := writeTestFile(t, t.TempDir(), "appended.tar.gz", append(validGz, []byte("garbage")...))
	appendedArchive, appendedErr := OpenTarGz(appended, Limits{})
	if appendedArchive != nil {
		_ = appendedArchive.Close()
	}
	assertKind(t, appendedErr, ErrCorrupt, ErrUnsupported)
}

func TestOpenTarGzEnforcesLimits(t *testing.T) {
	validPath := buildTarGz(t, []tarSample{{name: "a.txt", data: []byte("hello")}})
	cases := []struct {
		name    string
		path    string
		samples []tarSample
		limits  Limits
	}{
		{name: "归档大小上限", path: validPath, limits: Limits{MaxArchiveBytes: 10}},
		{name: "条目数上限", samples: []tarSample{{name: "a", data: []byte("1")}, {name: "b", data: []byte("2")}}, limits: Limits{MaxEntries: 1}},
		{name: "单文件上限", samples: []tarSample{{name: "big", data: []byte("0123456789")}}, limits: Limits{MaxFileBytes: 5}},
		{name: "解包总量上限", samples: []tarSample{{name: "a", data: []byte("0123456789")}, {name: "b", data: []byte("0123456789")}}, limits: Limits{MaxUnpackedBytes: 12}},
		{name: "元数据上限", samples: []tarSample{{name: "package.json", data: []byte("0123456789")}}, limits: Limits{MaxMetadataBytes: 5}},
		{name: "路径长度上限", samples: []tarSample{{name: "abcdef", data: []byte("x")}}, limits: Limits{MaxPathLength: 5}},
		{name: "压缩率上限", samples: []tarSample{{name: "zeros.bin", data: make([]byte, 2<<20)}}, limits: Limits{MaxCompressionRatio: 2}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := testCase.path
			if path == "" {
				path = buildTarGz(t, testCase.samples)
			}
			archive, err := OpenTarGz(path, testCase.limits)
			if archive != nil {
				_ = archive.Close()
			}
			assertKind(t, err, ErrLimitExceeded)
		})
	}
}

func TestOpenTarGzRejectsMalformedArchivePath(t *testing.T) {
	for name, path := range map[string]string{
		"不存在": filepath.Join(t.TempDir(), "missing.tar.gz"),
		"目录":  t.TempDir(),
		"空路径": "  ",
	} {
		t.Run(name, func(t *testing.T) {
			archive, err := OpenTarGz(path, Limits{})
			if archive != nil {
				_ = archive.Close()
			}
			assertKind(t, err, ErrCorrupt, ErrUnsupported)
		})
	}
}

func TestTarExtractDestinationRulesAndArchiveSwap(t *testing.T) {
	tarPath := buildTarGz(t, []tarSample{{name: "a.txt", data: []byte("hello")}})
	archive, err := OpenTarGz(tarPath, Limits{})
	if err != nil {
		t.Fatalf("OpenTarGz 失败: %v", err)
	}
	defer archive.Close()

	nonEmpty := filepath.Join(t.TempDir(), "non-empty")
	if err := os.MkdirAll(nonEmpty, 0o700); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nonEmpty, "keep"), []byte("keep"), 0o600); err != nil {
		t.Fatalf("写入文件失败: %v", err)
	}
	assertKind(t, archive.Extract(context.Background(), nonEmpty), ErrUnsafePath)

	// 打开后替换归档文件 → 拒绝解包。
	replacementBytes, err := os.ReadFile(buildTarGz(t, []tarSample{{name: "b.txt", data: []byte("replacement content longer")}}))
	if err != nil {
		t.Fatalf("读取替换归档失败: %v", err)
	}
	if err := os.WriteFile(tarPath, replacementBytes, 0o600); err != nil {
		t.Fatalf("替换归档失败: %v", err)
	}
	assertKind(t, archive.Extract(context.Background(), filepath.Join(t.TempDir(), "swapped")), ErrCorrupt)
}

func TestTarExtractContextCancelled(t *testing.T) {
	first := make([]byte, 256*1024)
	second := make([]byte, 256*1024)
	for index := range first {
		first[index] = byte(index)
		second[index] = byte(index + 1)
	}
	tarPath := buildTarGz(t, []tarSample{
		{name: "first.bin", data: first},
		{name: "second.bin", data: second},
	})
	archive, err := OpenTarGz(tarPath, Limits{})
	if err != nil {
		t.Fatalf("OpenTarGz 失败: %v", err)
	}
	defer archive.Close()
	dest := filepath.Join(t.TempDir(), "cancelled")
	ctx := &stepCancelContext{remaining: 5}
	err = archive.Extract(ctx, dest)
	assertKind(t, err, ErrCancelled)
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("取消后应清理目标目录，Stat err = %v", statErr)
	}
}

func TestTarExtractCancelledBeforeStart(t *testing.T) {
	tarPath := buildTarGz(t, []tarSample{{name: "a.txt", data: []byte("hello")}})
	archive, err := OpenTarGz(tarPath, Limits{})
	if err != nil {
		t.Fatalf("OpenTarGz 失败: %v", err)
	}
	defer archive.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dest := filepath.Join(t.TempDir(), "never-created")
	assertKind(t, archive.Extract(ctx, dest), ErrCancelled)
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("取消后不应创建目标目录，Stat err = %v", statErr)
	}
}
