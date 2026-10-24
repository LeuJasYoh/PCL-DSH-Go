// SPDX-License-Identifier: GPL-3.0-or-later

package safe

import (
	"io/fs"
	"strings"
	"testing"
)

func TestValidateEntriesAcceptsValidLayout(t *testing.T) {
	entries := []EntryInfo{
		{Name: "dspack.json", Size: 20, UncompressedSize: 20},
		{Name: "manifest.json", Size: 100, UncompressedSize: 100},
		{Name: "package.json", Size: 50, UncompressedSize: 50},
		{Name: "overrides/", IsDir: true, Mode: fs.ModeDir | 0o755},
		{Name: "overrides/skills/demo/SKILL.md", Size: 100, UncompressedSize: 100},
		{Name: "home/中文/文件.txt", Size: 100, UncompressedSize: 100},
		{Name: "profiles/web/package.json", Size: 50, UncompressedSize: 50},
		{Name: "empty", Size: 0, UncompressedSize: 0},
	}
	if err := ValidateEntries(entries, Limits{}); err != nil {
		t.Fatalf("合法条目被拒绝: %v", err)
	}
}

func TestValidateEntriesRejectsUnsafePaths(t *testing.T) {
	cases := []struct {
		name  string
		entry EntryInfo
	}{
		{"穿越", EntryInfo{Name: "overrides/../../escape", Size: 1, UncompressedSize: 1}},
		{"绝对路径", EntryInfo{Name: "/etc/passwd", Size: 1, UncompressedSize: 1}},
		{"盘符路径", EntryInfo{Name: "C:/evil", Size: 1, UncompressedSize: 1}},
		{"UNC 路径", EntryInfo{Name: "//server/share", Size: 1, UncompressedSize: 1}},
		{"反斜杠", EntryInfo{Name: "overrides\\bad", Size: 1, UncompressedSize: 1}},
		{"Windows 保留名", EntryInfo{Name: "overrides/NUL.txt", Size: 1, UncompressedSize: 1}},
		{"Windows 保留名 CON", EntryInfo{Name: "CON", Size: 1, UncompressedSize: 1}},
		{"Windows 保留名 COM1", EntryInfo{Name: "dir/COM1", Size: 1, UncompressedSize: 1}},
		{"尾随点", EntryInfo{Name: "overrides/bad.", Size: 1, UncompressedSize: 1}},
		{"尾随空格", EntryInfo{Name: "overrides/bad ", Size: 1, UncompressedSize: 1}},
		{"冒号", EntryInfo{Name: "overrides/a:b", Size: 1, UncompressedSize: 1}},
		{"点段", EntryInfo{Name: "overrides/./x", Size: 1, UncompressedSize: 1}},
		{"空段", EntryInfo{Name: "overrides//x", Size: 1, UncompressedSize: 1}},
		{"控制字符", EntryInfo{Name: "overrides/\x01bad", Size: 1, UncompressedSize: 1}},
		{"NUL 字符", EntryInfo{Name: "overrides/\x00bad", Size: 1, UncompressedSize: 1}},
		{"问号", EntryInfo{Name: "overrides/a?b", Size: 1, UncompressedSize: 1}},
		{"星号", EntryInfo{Name: "overrides/a*b", Size: 1, UncompressedSize: 1}},
		{"竖线", EntryInfo{Name: "overrides/a|b", Size: 1, UncompressedSize: 1}},
		{"尖括号", EntryInfo{Name: "overrides/a<b>", Size: 1, UncompressedSize: 1}},
		{"双引号", EntryInfo{Name: "overrides/a\"b", Size: 1, UncompressedSize: 1}},
		{"非法 UTF-8", EntryInfo{Name: "overrides/\xff\xfe", Size: 1, UncompressedSize: 1}},
		{"空路径", EntryInfo{Name: "", Size: 1, UncompressedSize: 1}},
		{"仅斜杠", EntryInfo{Name: "/", Size: 1, UncompressedSize: 1}},
		{"空白段", EntryInfo{Name: "overrides/   /x", Size: 1, UncompressedSize: 1}},
		{"组合字符", EntryInfo{Name: "cafe\u0301.txt", Size: 1, UncompressedSize: 1}},
		{"非 NFC 单例字符", EntryInfo{Name: "\u212b.txt", Size: 1, UncompressedSize: 1}},
		{"谚文字母", EntryInfo{Name: "\u1100\u1161.txt", Size: 1, UncompressedSize: 1}},
		{"非目录以斜杠结尾", EntryInfo{Name: "file/", IsDir: false, Size: 1, UncompressedSize: 1}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := ValidateEntries([]EntryInfo{testCase.entry}, Limits{})
			assertKind(t, err, ErrUnsafePath, ErrLimitExceeded)
		})
	}
}

func TestValidateEntriesRejectsConflicts(t *testing.T) {
	cases := []struct {
		name    string
		entries []EntryInfo
	}{
		{"重复路径", []EntryInfo{{Name: "a/b", Size: 1, UncompressedSize: 1}, {Name: "a/b", Size: 1, UncompressedSize: 1}}},
		{"大小写冲突", []EntryInfo{{Name: "a/B", Size: 1, UncompressedSize: 1}, {Name: "a/b", Size: 1, UncompressedSize: 1}}},
		{"目录大小写冲突", []EntryInfo{{Name: "A/x", Size: 1, UncompressedSize: 1}, {Name: "a/y", Size: 1, UncompressedSize: 1}}},
		{"文件与目录冲突一", []EntryInfo{{Name: "a/data", Size: 1, UncompressedSize: 1}, {Name: "a/data/file", Size: 1, UncompressedSize: 1}}},
		{"文件与目录冲突二", []EntryInfo{{Name: "a/data", Size: 1, UncompressedSize: 1}, {Name: "a/data/", IsDir: true}}},
		{"目录与文件冲突", []EntryInfo{{Name: "a/data/", IsDir: true}, {Name: "a/data", Size: 1, UncompressedSize: 1}}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assertKind(t, ValidateEntries(testCase.entries, Limits{}), ErrEntryConflict)
		})
	}
}

func TestValidateEntriesRejectsUnsupportedEntryKinds(t *testing.T) {
	cases := []struct {
		name  string
		entry EntryInfo
	}{
		{"符号链接", EntryInfo{Name: "link", Mode: fs.ModeSymlink | 0o777, Size: 7, UncompressedSize: 7}},
		{"设备文件", EntryInfo{Name: "dev", Mode: fs.ModeDevice | 0o644, Size: 0, UncompressedSize: 0}},
		{"字符设备", EntryInfo{Name: "char", Mode: fs.ModeDevice | fs.ModeCharDevice | 0o644}},
		{"命名管道", EntryInfo{Name: "fifo", Mode: fs.ModeNamedPipe | 0o644}},
		{"套接字", EntryInfo{Name: "sock", Mode: fs.ModeSocket | 0o644}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assertKind(t, ValidateEntries([]EntryInfo{testCase.entry}, Limits{}), ErrUnsupported)
		})
	}
}

func TestValidateEntriesEnforcesLimits(t *testing.T) {
	cases := []struct {
		name    string
		entries []EntryInfo
		limits  Limits
	}{
		{
			name:    "条目数上限",
			entries: []EntryInfo{{Name: "a", Size: 1, UncompressedSize: 1}, {Name: "b", Size: 1, UncompressedSize: 1}, {Name: "c", Size: 1, UncompressedSize: 1}},
			limits:  Limits{MaxEntries: 2},
		},
		{
			name:    "单文件上限",
			entries: []EntryInfo{{Name: "big", Size: 100, UncompressedSize: 100}},
			limits:  Limits{MaxFileBytes: 10},
		},
		{
			name:    "解包总量上限",
			entries: []EntryInfo{{Name: "a", Size: 20, UncompressedSize: 20}, {Name: "b", Size: 20, UncompressedSize: 20}},
			limits:  Limits{MaxUnpackedBytes: 30},
		},
		{
			name:    "元数据上限",
			entries: []EntryInfo{{Name: "manifest.json", Size: 50, UncompressedSize: 50}},
			limits:  Limits{MaxMetadataBytes: 10},
		},
		{
			name:    "路径长度上限",
			entries: []EntryInfo{{Name: "abcdefghij", Size: 1, UncompressedSize: 1}},
			limits:  Limits{MaxPathLength: 5},
		},
		{
			name:    "压缩率上限",
			entries: []EntryInfo{{Name: "bomb", Size: 10, UncompressedSize: 2 << 20}},
			limits:  Limits{MaxCompressionRatio: 100},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assertKind(t, ValidateEntries(testCase.entries, testCase.limits), ErrLimitExceeded)
		})
	}
}

func TestValidateEntriesRejectsNegativeAndDirSizes(t *testing.T) {
	assertKind(t, ValidateEntries([]EntryInfo{{Name: "a", UncompressedSize: -1}}, Limits{}), ErrCorrupt)
	assertKind(t, ValidateEntries([]EntryInfo{{Name: "a", Size: -1}}, Limits{}), ErrCorrupt)
	assertKind(t, ValidateEntries([]EntryInfo{{Name: "dir/", IsDir: true, UncompressedSize: 1}}, Limits{}), ErrCorrupt)
}

func TestKindOfAndErrorMessage(t *testing.T) {
	err := unsafePathf("坏路径: %q", "x")
	if KindOf(err) != ErrUnsafePath {
		t.Fatalf("KindOf = %q", KindOf(err))
	}
	if !strings.Contains(err.Error(), "unsafe_path") || !strings.Contains(err.Error(), "坏路径") {
		t.Fatalf("Error() = %q", err.Error())
	}
	var nilError *Error
	if nilError.Error() != "<nil>" {
		t.Fatalf("nil Error() = %q", nilError.Error())
	}
	if KindOf(nil) != "" {
		t.Fatal("KindOf(nil) 应为空")
	}
	if KindOf(fs.ErrNotExist) != "" {
		t.Fatal("KindOf(非本包错误) 应为空")
	}
}
