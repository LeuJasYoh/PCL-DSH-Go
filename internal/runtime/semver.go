// SPDX-License-Identifier: MIT

package runtime

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// maxVersionLength bounds version strings before they are used as path
// elements or cache file names.
const maxVersionLength = 160

// versionPattern is SemVer 2.0.0 with the grammar spelled without backslashes
// ([.] and [+] instead of \. and \+) so it stays readable in source.
var versionPattern = regexp.MustCompile("^(0|[1-9][0-9]*)[.](0|[1-9][0-9]*)[.](0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:[.][0-9A-Za-z-]+)*))?(?:[+]([0-9A-Za-z-]+(?:[.][0-9A-Za-z-]+)*))?$")

// Version is one parsed SemVer 2.0.0 version.
//
// Build metadata is parsed and kept (Raw/String), but it never participates in
// precedence, exactly as mandated by SemVer 2.0.0. Version values are immutable.
type Version struct {
	Major uint64
	Minor uint64
	Patch uint64
	// Pre holds the dot-separated pre-release identifiers; empty for releases.
	Pre []string
	// Build holds the build metadata without the leading '+' (may be empty).
	Build string
	// Raw is the original version string.
	Raw string
}

// String returns the original version string.
func (v Version) String() string { return v.Raw }

// IsPrerelease reports whether the version carries pre-release identifiers.
func (v Version) IsPrerelease() bool { return len(v.Pre) > 0 }

// Compare returns -1, 0 or +1 when v sorts before, equal to, or after other.
//
// Precedence follows SemVer 2.0.0 ("1.0.0-alpha" < "1.0.0-alpha.1" <
// "1.0.0-alpha.beta" < "1.0.0-beta" < "1.0.0-beta.2" < "1.0.0-beta.11" <
// "1.0.0-rc.1" < "1.0.0"). Build metadata is ignored. Versions with equal
// precedence but different build metadata (or a different textual spelling) are
// ordered by their raw string so sorting stays total and deterministic.
func (v Version) Compare(other Version) int {
	if c := compareUint(v.Major, other.Major); c != 0 {
		return c
	}
	if c := compareUint(v.Minor, other.Minor); c != 0 {
		return c
	}
	if c := compareUint(v.Patch, other.Patch); c != 0 {
		return c
	}
	if c := comparePrerelease(v.Pre, other.Pre); c != 0 {
		return c
	}
	return strings.Compare(v.Raw, other.Raw)
}

// ParseVersion parses a strict SemVer 2.0.0 version. A leading 'v' prefix is
// rejected; npm registry version keys never carry one.
func ParseVersion(raw string) (Version, error) {
	if len(raw) == 0 || len(raw) > maxVersionLength {
		return Version{}, fmt.Errorf("版本号 %q 无效：必须是完整的 SemVer 版本（例如 0.2.1-rc.3）", raw)
	}
	match := versionPattern.FindStringSubmatch(raw)
	if match == nil {
		return Version{}, fmt.Errorf("版本号 %q 无效：必须是完整的 SemVer 版本（例如 0.2.1-rc.3）", raw)
	}
	major, err := parseUintField(match[1])
	if err != nil {
		return Version{}, fmt.Errorf("版本号 %q 无效：%w", raw, err)
	}
	minor, err := parseUintField(match[2])
	if err != nil {
		return Version{}, fmt.Errorf("版本号 %q 无效：%w", raw, err)
	}
	patch, err := parseUintField(match[3])
	if err != nil {
		return Version{}, fmt.Errorf("版本号 %q 无效：%w", raw, err)
	}
	version := Version{Major: major, Minor: minor, Patch: patch, Build: match[5], Raw: raw}
	if match[4] != "" {
		version.Pre = strings.Split(match[4], ".")
		for _, identifier := range version.Pre {
			// SemVer 2.0.0 requires numeric pre-release identifiers to have no
			// leading zeroes. npm enforces this for published versions.
			if len(identifier) > 1 && identifier[0] == '0' && isNumericIdentifier(identifier) {
				return Version{}, fmt.Errorf("版本号 %q 无效：预发布标识 %q 不能有前导零", raw, identifier)
			}
		}
	}
	return version, nil
}

// IsVersion reports whether raw is a strict SemVer 2.0.0 version.
func IsVersion(raw string) bool {
	_, err := ParseVersion(raw)
	return err == nil
}

// SortVersionsDescending returns the valid SemVer entries of versions sorted by
// descending precedence. Invalid entries are dropped (callers pass registry keys
// which occasionally contain dist-tags such as "latest" or "next"). Equal
// precedence is tie-broken by raw string, so the result is deterministic.
func SortVersionsDescending(versions []string) []string {
	parsed := make([]Version, 0, len(versions))
	for _, raw := range versions {
		version, err := ParseVersion(raw)
		if err != nil {
			continue
		}
		parsed = append(parsed, version)
	}
	sort.SliceStable(parsed, func(i, j int) bool { return parsed[i].Compare(parsed[j]) > 0 })
	result := make([]string, len(parsed))
	for i, version := range parsed {
		result[i] = version.Raw
	}
	return result
}

func parseUintField(text string) (uint64, error) {
	var value uint64
	for i := 0; i < len(text); i++ {
		digit := text[i] - '0'
		if value > (^uint64(0)-uint64(digit))/10 {
			return 0, fmt.Errorf("数值 %s 超出支持范围", text)
		}
		value = value*10 + uint64(digit)
	}
	return value, nil
}

func compareUint(left, right uint64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

// comparePrerelease implements SemVer 2.0.0 item 11.4 priority rules.
func comparePrerelease(left, right []string) int {
	switch {
	case len(left) == 0 && len(right) == 0:
		return 0
	case len(left) == 0:
		// A release has higher precedence than any pre-release of the same
		// major.minor.patch.
		return 1
	case len(right) == 0:
		return -1
	}
	for i := 0; i < len(left) && i < len(right); i++ {
		leftNumeric := isNumericIdentifier(left[i])
		rightNumeric := isNumericIdentifier(right[i])
		switch {
		case leftNumeric && rightNumeric:
			if c := compareNumericIdentifier(left[i], right[i]); c != 0 {
				return c
			}
		case leftNumeric:
			// Numeric identifiers always have lower precedence than
			// alphanumeric identifiers.
			return -1
		case rightNumeric:
			return 1
		default:
			if c := strings.Compare(left[i], right[i]); c != 0 {
				return c
			}
		}
	}
	return compareUint(uint64(len(left)), uint64(len(right)))
}

func isNumericIdentifier(identifier string) bool {
	if identifier == "" {
		return false
	}
	for i := 0; i < len(identifier); i++ {
		if identifier[i] < '0' || identifier[i] > '9' {
			return false
		}
	}
	return true
}

// compareNumericIdentifier compares two all-digit identifiers of arbitrary
// length without overflowing.
func compareNumericIdentifier(left, right string) int {
	left = strings.TrimLeft(left, "0")
	right = strings.TrimLeft(right, "0")
	switch {
	case len(left) < len(right):
		return -1
	case len(left) > len(right):
		return 1
	default:
		return strings.Compare(left, right)
	}
}
