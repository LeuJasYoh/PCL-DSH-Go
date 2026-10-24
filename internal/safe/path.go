// SPDX-License-Identifier: GPL-3.0-or-later

package safe

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// windowsReservedRE 匹配 Windows 保留名称（可带扩展名）。
var windowsReservedRE = regexp.MustCompile("(?i)^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(\\..*)?$")

// metadataNames 是受 4 MiB 元数据上限约束的文件名（取路径 basename）。
var metadataNames = map[string]struct{}{
	"dspack.json":       {},
	"manifest.json":     {},
	"package.json":      {},
	"dsh-snapshot.json": {},
	"profile.yaml":      {},
}

// nonNFCChars 是已知具有单例规范分解、且本身不是 NFC 的字符；
// 不引入 x/text 依赖，因此用显式拒绝覆盖最常见的归一化冲突来源。
var nonNFCChars = map[rune]struct{}{
	'\u2126': {}, // OHM SIGN -> U+03A9
	'\u212A': {}, // KELVIN SIGN -> U+004B
	'\u212B': {}, // ANGSTROM SIGN -> U+00C5
	'\u0374': {}, // GREEK NUMERAL SIGN -> U+02B9
	'\u037E': {}, // GREEK QUESTION MARK -> U+003B
	'\u0387': {}, // GREEK ANO TELEIA -> U+00B7
	'\u1FBE': {}, // GREEK PROSGEGRAMMENI -> U+03B9
}

// indexValidator 维护单个归档内的路径/冲突/限值状态（按条目顺序调用 add）。
type indexValidator struct {
	limits Limits
	seen   map[string]string // pathKey -> 首次出现的清洗后名称
	casing map[string]string // pathKey -> 首次出现的前缀拼写
	files  map[string]struct{}
	dirs   map[string]struct{}
	total  int64
	count  int
}

func newIndexValidator(limits Limits) *indexValidator {
	return &indexValidator{
		limits: limits,
		seen:   make(map[string]string),
		casing: make(map[string]string),
		files:  make(map[string]struct{}),
		dirs:   make(map[string]struct{}),
	}
}

// Total 返回已累计的解压后字节数。
func (v *indexValidator) Total() int64 { return v.total }

// add 校验一个条目并登记其路径；返回清洗后的路径（目录去掉结尾斜杠）。
func (v *indexValidator) add(entry EntryInfo) (string, error) {
	v.count++
	if v.count > v.limits.MaxEntries {
		return "", limitErrorf("归档条目数超过上限 %d", v.limits.MaxEntries)
	}
	clean, isDir, err := normalizeEntryName(entry.Name, entry.IsDir, v.limits)
	if err != nil {
		return "", err
	}
	if err := validateEntryMode(entry); err != nil {
		return "", err
	}
	if entry.Size < 0 || entry.UncompressedSize < 0 {
		return "", corruptf("条目 %q 的大小为负值", entry.Name)
	}
	if isDir {
		if entry.UncompressedSize != 0 {
			return "", corruptf("目录条目 %q 的解压大小非零（%d）", entry.Name, entry.UncompressedSize)
		}
	} else {
		if entry.UncompressedSize > v.limits.MaxFileBytes {
			return "", limitErrorf("文件 %q 解压后大小 %d 超过单文件上限 %d", entry.Name, entry.UncompressedSize, v.limits.MaxFileBytes)
		}
		if isMetadataFile(clean) && entry.UncompressedSize > v.limits.MaxMetadataBytes {
			return "", limitErrorf("元数据文件 %q 大小 %d 超过上限 %d", entry.Name, entry.UncompressedSize, v.limits.MaxMetadataBytes)
		}
		if err := v.checkCompressionRatio(entry); err != nil {
			return "", err
		}
		if entry.UncompressedSize > v.limits.MaxUnpackedBytes-v.total {
			return "", limitErrorf("归档解压总量超过上限 %d 字节", v.limits.MaxUnpackedBytes)
		}
		v.total += entry.UncompressedSize
	}

	key := pathKey(clean)
	if first, ok := v.seen[key]; ok {
		return "", conflictf("归档路径重复或大小写/Unicode 归一冲突: %q（与 %q 冲突）", clean, first)
	}
	v.seen[key] = clean

	parts := strings.Split(clean, "/")
	current := ""
	for index, part := range parts {
		if index > 0 {
			current += "/"
		}
		current += part
		prefixKey := pathKey(current)
		if first, ok := v.casing[prefixKey]; ok {
			if first != current {
				return "", conflictf("归档路径大小写/Unicode 归一冲突: %q（与 %q 冲突）", clean, first)
			}
		} else {
			v.casing[prefixKey] = current
		}
		if index < len(parts)-1 {
			if _, isFile := v.files[prefixKey]; isFile {
				return "", conflictf("归档内文件与目录冲突: %q", clean)
			}
			v.dirs[prefixKey] = struct{}{}
		}
	}
	if isDir {
		if _, isFile := v.files[key]; isFile {
			return "", conflictf("归档内文件与目录冲突: %q", clean)
		}
		v.dirs[key] = struct{}{}
	} else {
		if _, isDir := v.dirs[key]; isDir {
			return "", conflictf("归档内文件与目录冲突: %q", clean)
		}
		v.files[key] = struct{}{}
	}
	return clean, nil
}

// checkCompressionRatio 校验压缩率：仅当解压后 >1 MiB 且已知压缩大小时判定。
func (v *indexValidator) checkCompressionRatio(entry EntryInfo) error {
	if entry.UncompressedSize <= compressionRatioThreshold {
		return nil
	}
	if entry.Size <= 0 {
		// TAR/已解压 staging 不提供单条目压缩大小；聚合压缩率在 TAR 扫描阶段判定。
		return nil
	}
	if entry.UncompressedSize/entry.Size > v.limits.MaxCompressionRatio {
		return limitErrorf("文件 %q 压缩率超过上限 %d", entry.Name, v.limits.MaxCompressionRatio)
	}
	return nil
}

// normalizeEntryName 校验原始归档路径并返回清洗后的相对路径（目录去掉结尾斜杠）。
func normalizeEntryName(raw string, isDir bool, limits Limits) (string, bool, error) {
	if raw == "" {
		return "", false, unsafePathf("归档路径为空")
	}
	if !utf8.ValidString(raw) {
		return "", false, unsafePathf("归档路径不是合法 UTF-8: %q", raw)
	}
	if strings.Contains(raw, "\\") {
		return "", false, unsafePathf("归档路径包含反斜杠: %q", raw)
	}
	if strings.HasPrefix(raw, "/") {
		return "", false, unsafePathf("归档路径不能是绝对路径: %q", raw)
	}
	if strings.HasSuffix(raw, "/") {
		if !isDir {
			return "", false, unsafePathf("非目录条目不能以 / 结尾: %q", raw)
		}
		raw = strings.TrimSuffix(raw, "/")
		if raw == "" {
			return "", false, unsafePathf("归档路径为空")
		}
	}
	if len(raw) > limits.MaxPathLength {
		return "", false, limitErrorf("归档路径长度 %d 超过上限 %d: %q", len(raw), limits.MaxPathLength, raw)
	}
	for _, segment := range strings.Split(raw, "/") {
		if err := validatePathSegment(segment, raw); err != nil {
			return "", false, err
		}
	}
	return raw, isDir, nil
}

// validatePathSegment 校验单个路径段的可移植性与 Windows 兼容性。
func validatePathSegment(segment, fullPath string) error {
	if segment == "" {
		return unsafePathf("归档路径包含空段: %q", fullPath)
	}
	if segment == "." || segment == ".." {
		return unsafePathf("归档路径包含 . 或 .. 段: %q", fullPath)
	}
	if strings.TrimSpace(segment) == "" {
		return unsafePathf("归档路径包含空白段: %q", fullPath)
	}
	if strings.HasSuffix(segment, ".") || strings.HasSuffix(segment, " ") {
		return unsafePathf("归档路径段不能以点或空格结尾: %q", fullPath)
	}
	if windowsReservedRE.MatchString(segment) {
		return unsafePathf("归档路径包含 Windows 保留名称: %q", fullPath)
	}
	for _, r := range segment {
		switch {
		case unicode.IsControl(r):
			return unsafePathf("归档路径包含控制字符: %q", fullPath)
		case r == '<' || r == '>' || r == ':' || r == '"' || r == '|' || r == '?' || r == '*':
			return unsafePathf("归档路径包含不可移植字符: %q", fullPath)
		case unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r):
			return unsafePathf("归档路径包含组合字符（可能造成 Unicode 归一冲突）: %q", fullPath)
		case isHangulJamo(r):
			return unsafePathf("归档路径包含可归一组合的谚文字母: %q", fullPath)
		}
		if _, bad := nonNFCChars[r]; bad {
			return unsafePathf("归档路径包含非 NFC 规范化字符: %q", fullPath)
		}
	}
	return nil
}

// isHangulJamo 判断是否为可与前导字符组合成 NFC 音节的谚文字母。
func isHangulJamo(r rune) bool {
	return (r >= 0x1100 && r <= 0x11FF) || (r >= 0xA960 && r <= 0xA97F) || (r >= 0xD7B0 && r <= 0xD7FF)
}

// validateEntryMode 拒绝符号链接与设备/管道/套接字等特殊文件。
func validateEntryMode(entry EntryInfo) error {
	mode := entry.Mode
	switch {
	case mode&fs.ModeSymlink != 0:
		return unsupportedf("归档条目是符号链接（不允许）: %q", entry.Name)
	case mode&(fs.ModeDevice|fs.ModeCharDevice|fs.ModeNamedPipe|fs.ModeSocket|fs.ModeIrregular) != 0:
		return unsupportedf("归档条目是特殊文件（不允许）: %q", entry.Name)
	}
	return nil
}

// isMetadataFile 判断清洗后的路径是否属于受元数据上限约束的文件。
func isMetadataFile(cleanPath string) bool {
	_, ok := metadataNames[path.Base(cleanPath)]
	return ok
}

// pathKey 生成大小写不敏感的比较键（组合字符已在上层拒绝，故无需额外归一化）。
func pathKey(name string) string {
	return strings.ToLower(name)
}
