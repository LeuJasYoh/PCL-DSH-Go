// SPDX-License-Identifier: MIT

package safe

import (
	"context"
	"hash/crc32"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenZipValidArchiveAndExtract(t *testing.T) {
	zipPath := buildZip(t, []zipSample{
		{name: "dspack.json", data: []byte("{\"format\":\"dspack\",\"version\":3}")},
		{name: "manifest.json", data: []byte("{\"manifestVersion\":5}")},
		{name: "overrides/", mode: fs.ModeDir | 0o755},
		{name: "overrides/skills/demo/SKILL.md", data: []byte("# safe skill")},
		{name: "home/中文/文件.txt", data: []byte("内容")},
	})
	archive, err := OpenZip(zipPath, Limits{})
	if err != nil {
		t.Fatalf("OpenZip 合法归档失败: %v", err)
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
	// Entries 返回拷贝。
	entries[0].Name = "mutated"
	if archive.Entries()[0].Name == "mutated" {
		t.Fatal("Entries 应返回拷贝")
	}

	dest := filepath.Join(t.TempDir(), "out")
	if err := archive.Extract(context.Background(), dest); err != nil {
		t.Fatalf("Extract 失败: %v", err)
	}
	readExtracted := func(relative, want string) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", relative, err)
		}
		if string(data) != want {
			t.Fatalf("%s 内容 = %q，期望 %q", relative, string(data), want)
		}
	}
	readExtracted("overrides/skills/demo/SKILL.md", "# safe skill")
	readExtracted("home/中文/文件.txt", "内容")
}

func TestOpenZipEmptyArchive(t *testing.T) {
	zipPath := buildZip(t, nil)
	archive, err := OpenZip(zipPath, Limits{})
	if err != nil {
		t.Fatalf("OpenZip 空归档失败: %v", err)
	}
	defer archive.Close()
	if got := len(archive.Entries()); got != 0 {
		t.Fatalf("条目数 = %d，期望 0", got)
	}
	dest := filepath.Join(t.TempDir(), "empty-out")
	if err := archive.Extract(context.Background(), dest); err != nil {
		t.Fatalf("Extract 空归档失败: %v", err)
	}
	if info, err := os.Stat(dest); err != nil || !info.IsDir() {
		t.Fatalf("解包目标目录未创建: %v", err)
	}
}

// TestOpenZipRejectsUnsafePaths 覆盖 20+ 类恶意/畸形 ZIP 路径。
func TestOpenZipRejectsUnsafePaths(t *testing.T) {
	cases := []struct {
		name  string
		entry rawZipEntry
		limit Limits
	}{
		{"穿越路径", newRawZipEntry("overrides/../../escape", []byte("x")), Limits{}},
		{"绝对路径", newRawZipEntry("/etc/passwd", []byte("x")), Limits{}},
		{"盘符路径", newRawZipEntry("C:/evil", []byte("x")), Limits{}},
		{"UNC 路径", newRawZipEntry("//server/share", []byte("x")), Limits{}},
		{"反斜杠路径", newRawZipEntry("overrides\\bad.txt", []byte("x")), Limits{}},
		{"Windows 保留名 NUL", newRawZipEntry("overrides/NUL.txt", []byte("x")), Limits{}},
		{"Windows 保留名 CON", newRawZipEntry("CON", []byte("x")), Limits{}},
		{"Windows 保留名 COM1", newRawZipEntry("dir/COM1", []byte("x")), Limits{}},
		{"Windows 保留名 LPT9.log", newRawZipEntry("dir/LPT9.log", []byte("x")), Limits{}},
		{"尾随点", newRawZipEntry("overrides/bad.", []byte("x")), Limits{}},
		{"尾随空格", newRawZipEntry("overrides/bad ", []byte("x")), Limits{}},
		{"冒号", newRawZipEntry("overrides/a:b", []byte("x")), Limits{}},
		{"点段", newRawZipEntry("overrides/./x", []byte("x")), Limits{}},
		{"空段", newRawZipEntry("overrides//x", []byte("x")), Limits{}},
		{"控制字符", newRawZipEntry("overrides/\x01bad", []byte("x")), Limits{}},
		{"NUL 字符", newRawZipEntry("overrides/\x00bad", []byte("x")), Limits{}},
		{"问号", newRawZipEntry("overrides/a?b", []byte("x")), Limits{}},
		{"星号", newRawZipEntry("overrides/a*b", []byte("x")), Limits{}},
		{"竖线", newRawZipEntry("overrides/a|b", []byte("x")), Limits{}},
		{"尖括号", newRawZipEntry("overrides/a<b>", []byte("x")), Limits{}},
		{"双引号", newRawZipEntry("overrides/a\"b", []byte("x")), Limits{}},
		{"组合字符", newRawZipEntry("cafe\u0301.txt", []byte("x")), Limits{}},
		{"非 NFC 单例字符", newRawZipEntry("\u212b.txt", []byte("x")), Limits{}},
		{"超长路径", newRawZipEntry(strings.Repeat("a", 40), []byte("x")), Limits{MaxPathLength: 10}},
		{"空路径", newRawZipEntry("", []byte("x")), Limits{}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			zipPath := buildRawZip(t, []rawZipEntry{testCase.entry})
			archive, err := OpenZip(zipPath, testCase.limit)
			if archive != nil {
				_ = archive.Close()
			}
			if testCase.name == "空路径" {
				// 空名称可能被 zip 读取器拒绝（同样属于错误）。
				if err == nil {
					t.Fatal("空路径应被拒绝")
				}
				return
			}
			assertKind(t, err, ErrUnsafePath, ErrLimitExceeded)
		})
	}
}

func TestOpenZipRejectsConflicts(t *testing.T) {
	cases := []struct {
		name    string
		entries []rawZipEntry
	}{
		{"重复路径", []rawZipEntry{newRawZipEntry("a/b", []byte("1")), newRawZipEntry("a/b", []byte("2"))}},
		{"大小写冲突", []rawZipEntry{newRawZipEntry("A/x", []byte("1")), newRawZipEntry("a/y", []byte("2"))}},
		{"文件目录冲突", []rawZipEntry{newRawZipEntry("a/data", []byte("1")), newRawZipEntry("a/data/file", []byte("2"))}},
		{"目录文件冲突", []rawZipEntry{dirRawZipEntry("a"), newRawZipEntry("a", []byte("1"))}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			zipPath := buildRawZip(t, testCase.entries)
			archive, err := OpenZip(zipPath, Limits{})
			if archive != nil {
				_ = archive.Close()
			}
			assertKind(t, err, ErrEntryConflict)
		})
	}
}

func dirRawZipEntry(name string) rawZipEntry {
	entry := newRawZipEntry(name, nil)
	entry.mode = 0o040755
	return entry
}

func TestOpenZipRejectsLinksAndSpecialFiles(t *testing.T) {
	cases := []struct {
		name string
		mode uint32
	}{
		{"符号链接", 0o120777},
		{"字符设备", 0o020644},
		{"块设备", 0o060644},
		{"命名管道", 0o010644},
		{"套接字", 0o140644},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			entry := newRawZipEntry("entry", []byte("target"))
			entry.mode = testCase.mode
			zipPath := buildRawZip(t, []rawZipEntry{entry})
			archive, err := OpenZip(zipPath, Limits{})
			if archive != nil {
				_ = archive.Close()
			}
			assertKind(t, err, ErrUnsupported)
		})
	}
}

func TestOpenZipRejectsEncryptionAndUnsupportedCompression(t *testing.T) {
	encrypted := newRawZipEntry("secret.txt", []byte("data"))
	encrypted.flags = 0x1
	zipPath := buildRawZip(t, []rawZipEntry{encrypted})
	archive, err := OpenZip(zipPath, Limits{})
	if archive != nil {
		_ = archive.Close()
	}
	assertKind(t, err, ErrUnsupported)

	unsupportedMethod := newRawZipEntry("method.bin", []byte("data"))
	unsupportedMethod.method = 12 // bzip2
	zipPath = buildRawZip(t, []rawZipEntry{unsupportedMethod})
	archive, err = OpenZip(zipPath, Limits{})
	if archive != nil {
		_ = archive.Close()
	}
	assertKind(t, err, ErrUnsupported)
}

func TestOpenZipEnforcesLimits(t *testing.T) {
	validZip := buildZip(t, []zipSample{{name: "a.txt", data: []byte("hello")}})

	cases := []struct {
		name    string
		path    string
		limits  Limits
		entries []rawZipEntry
	}{
		{
			name:   "归档大小上限",
			path:   validZip,
			limits: Limits{MaxArchiveBytes: 10},
		},
		{
			name:    "条目数上限",
			limits:  Limits{MaxEntries: 1},
			entries: []rawZipEntry{newRawZipEntry("a", []byte("1")), newRawZipEntry("b", []byte("2"))},
		},
		{
			name:    "单文件上限",
			limits:  Limits{MaxFileBytes: 5},
			entries: []rawZipEntry{newRawZipEntry("big", []byte("0123456789"))},
		},
		{
			name:    "解包总量上限",
			limits:  Limits{MaxUnpackedBytes: 12},
			entries: []rawZipEntry{newRawZipEntry("a", []byte("0123456789")), newRawZipEntry("b", []byte("0123456789"))},
		},
		{
			name:    "元数据上限",
			limits:  Limits{MaxMetadataBytes: 5},
			entries: []rawZipEntry{newRawZipEntry("manifest.json", []byte("0123456789"))},
		},
		{
			name:   "压缩率上限",
			limits: Limits{MaxCompressionRatio: 100},
			entries: func() []rawZipEntry {
				entry := newRawZipEntry("bomb", []byte("tiny"))
				entry.uncompressed = int64(2 << 20)
				entry.compressed = 100
				return []rawZipEntry{entry}
			}(),
		},
		{
			name:   "零压缩大小炸弹",
			limits: Limits{},
			entries: func() []rawZipEntry {
				entry := newRawZipEntry("bomb", nil)
				entry.uncompressed = int64(4 << 20)
				entry.compressed = 0
				return []rawZipEntry{entry}
			}(),
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := testCase.path
			if path == "" {
				path = buildRawZip(t, testCase.entries)
			}
			archive, err := OpenZip(path, testCase.limits)
			if archive != nil {
				_ = archive.Close()
			}
			assertKind(t, err, ErrLimitExceeded)
		})
	}
}

func TestOpenZipRejectsMalformedContainer(t *testing.T) {
	validZip := buildZip(t, []zipSample{{name: "a.txt", data: []byte("hello")}})
	validBytes, err := os.ReadFile(validZip)
	if err != nil {
		t.Fatalf("读取测试 ZIP 失败: %v", err)
	}
	truncated := writeTestFile(t, t.TempDir(), "truncated.zip", validBytes[:len(validBytes)/2])
	garbage := writeTestFile(t, t.TempDir(), "garbage.zip", []byte("PK\x03\x04not-a-real-zip"))
	empty := writeTestFile(t, t.TempDir(), "empty.zip", nil)

	for name, path := range map[string]string{
		"截断":   truncated,
		"垃圾数据": garbage,
		"空文件":  empty,
		"不存在":  filepath.Join(t.TempDir(), "missing.zip"),
		"目录":   t.TempDir(),
	} {
		t.Run(name, func(t *testing.T) {
			archive, err := OpenZip(path, Limits{})
			if archive != nil {
				_ = archive.Close()
			}
			assertKind(t, err, ErrCorrupt, ErrUnsupported)
		})
	}
}

func TestZipExtractVerifiesCRCAndCleansUp(t *testing.T) {
	payload := []byte("crc-mismatch-payload")
	entry := newRawZipEntry("file.txt", payload)
	entry.crcOverride = crc32.ChecksumIEEE(payload) ^ 0xFFFFFFFF
	zipPath := buildRawZip(t, []rawZipEntry{entry})
	archive, err := OpenZip(zipPath, Limits{})
	if err != nil {
		t.Fatalf("OpenZip 不应读取数据而失败: %v", err)
	}
	defer archive.Close()
	dest := filepath.Join(t.TempDir(), "crc-out")
	err = archive.Extract(context.Background(), dest)
	assertKind(t, err, ErrCorrupt)
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("解包失败后应清理目标目录，Stat err = %v", statErr)
	}
}

func TestZipExtractDestinationRules(t *testing.T) {
	zipPath := buildZip(t, []zipSample{{name: "a.txt", data: []byte("hello")}})
	archive, err := OpenZip(zipPath, Limits{})
	if err != nil {
		t.Fatalf("OpenZip 失败: %v", err)
	}
	defer archive.Close()

	nonEmpty := filepath.Join(t.TempDir(), "non-empty")
	if err := os.MkdirAll(nonEmpty, 0o700); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nonEmpty, "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatalf("写入文件失败: %v", err)
	}
	assertKind(t, archive.Extract(context.Background(), nonEmpty), ErrUnsafePath)

	asFile := writeTestFile(t, t.TempDir(), "target-file", []byte("x"))
	assertKind(t, archive.Extract(context.Background(), asFile), ErrUnsafePath)

	emptyDest := filepath.Join(t.TempDir(), "empty")
	if err := os.MkdirAll(emptyDest, 0o700); err != nil {
		t.Fatalf("创建空目录失败: %v", err)
	}
	if err := archive.Extract(context.Background(), emptyDest); err != nil {
		t.Fatalf("解包到已存在的空目录失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(emptyDest, "a.txt")); err != nil {
		t.Fatalf("解包文件缺失: %v", err)
	}
}

func TestZipExtractContextCancelled(t *testing.T) {
	large := make([]byte, 256*1024)
	for index := range large {
		large[index] = byte(index)
	}
	zipPath := buildZip(t, []zipSample{{name: "large.bin", data: large, method: 0}})
	archive, err := OpenZip(zipPath, Limits{})
	if err != nil {
		t.Fatalf("OpenZip 失败: %v", err)
	}
	defer archive.Close()
	dest := filepath.Join(t.TempDir(), "cancelled")
	ctx := &stepCancelContext{remaining: 4}
	err = archive.Extract(ctx, dest)
	assertKind(t, err, ErrCancelled)
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("取消后应清理目标目录，Stat err = %v", statErr)
	}
}

func TestZipExtractCancelledBeforeStart(t *testing.T) {
	zipPath := buildZip(t, []zipSample{{name: "a.txt", data: []byte("hello")}})
	archive, err := OpenZip(zipPath, Limits{})
	if err != nil {
		t.Fatalf("OpenZip 失败: %v", err)
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

func TestZipExtractClosedArchive(t *testing.T) {
	zipPath := buildZip(t, []zipSample{{name: "a.txt", data: []byte("hello")}})
	archive, err := OpenZip(zipPath, Limits{})
	if err != nil {
		t.Fatalf("OpenZip 失败: %v", err)
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("重复 Close 失败: %v", err)
	}
	assertKind(t, archive.Extract(context.Background(), filepath.Join(t.TempDir(), "out")), ErrCorrupt)
}
