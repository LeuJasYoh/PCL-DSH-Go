// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"reflect"
	"testing"
)

func TestParseVersion(t *testing.T) {
	t.Parallel()
	valid := []struct {
		raw   string
		major uint64
		minor uint64
		patch uint64
		pre   []string
		build string
	}{
		{raw: "0.2.1-rc.3", major: 0, minor: 2, patch: 1, pre: []string{"rc", "3"}},
		{raw: "0.2.1-rc.10", major: 0, minor: 2, patch: 1, pre: []string{"rc", "10"}},
		{raw: "0.2.1", major: 0, minor: 2, patch: 1},
		{raw: "1.0.0-alpha.beta.1", major: 1, pre: []string{"alpha", "beta", "1"}},
		{raw: "10.20.30+build.5", major: 10, minor: 20, patch: 30, build: "build.5"},
		{raw: "0.2.1-rc.3+sha.abc123", major: 0, minor: 2, patch: 1, pre: []string{"rc", "3"}, build: "sha.abc123"},
	}
	for _, tc := range valid {
		tc := tc
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()
			version, err := ParseVersion(tc.raw)
			if err != nil {
				t.Fatalf("ParseVersion(%q) error = %v", tc.raw, err)
			}
			if version.Major != tc.major || version.Minor != tc.minor || version.Patch != tc.patch {
				t.Fatalf("ParseVersion(%q) = %d.%d.%d, want %d.%d.%d", tc.raw, version.Major, version.Minor, version.Patch, tc.major, tc.minor, tc.patch)
			}
			if !reflect.DeepEqual(version.Pre, tc.pre) {
				t.Fatalf("ParseVersion(%q).Pre = %#v, want %#v", tc.raw, version.Pre, tc.pre)
			}
			if version.Build != tc.build {
				t.Fatalf("ParseVersion(%q).Build = %q, want %q", tc.raw, version.Build, tc.build)
			}
			if version.String() != tc.raw {
				t.Fatalf("String() = %q, want %q", version.String(), tc.raw)
			}
			if got, want := version.IsPrerelease(), len(tc.pre) > 0; got != want {
				t.Fatalf("IsPrerelease() = %v, want %v", got, want)
			}
			if !IsVersion(tc.raw) {
				t.Fatalf("IsVersion(%q) = false, want true", tc.raw)
			}
		})
	}

	invalid := []string{
		"",
		"1",
		"1.2",
		"1.2.3.4",
		"v1.2.3",
		"V1.2.3",
		"01.2.3",
		"1.02.3",
		"1.2.03",
		"1.2.3-",
		"1.2.3+",
		"1.2.3-01",
		"1.2.3-alpha.01",
		"1.2.3-..",
		"1.2.3+build..1",
		"latest",
		"1.2.3 ",
		" 1.2.3",
		"18446744073709551616.0.0",
	}
	for _, raw := range invalid {
		raw := raw
		t.Run("invalid_"+raw, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseVersion(raw); err == nil {
				t.Fatalf("ParseVersion(%q) succeeded, want error", raw)
			}
			if IsVersion(raw) {
				t.Fatalf("IsVersion(%q) = true, want false", raw)
			}
		})
	}
}

func TestVersionCompareSemVerPriorityChain(t *testing.T) {
	t.Parallel()
	ascending := []string{
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-alpha.beta",
		"1.0.0-beta",
		"1.0.0-beta.2",
		"1.0.0-beta.11",
		"1.0.0-rc.1",
		"1.0.0",
	}
	for i := 0; i < len(ascending); i++ {
		for j := 0; j < len(ascending); j++ {
			left, err := ParseVersion(ascending[i])
			if err != nil {
				t.Fatalf("ParseVersion(%q): %v", ascending[i], err)
			}
			right, err := ParseVersion(ascending[j])
			if err != nil {
				t.Fatalf("ParseVersion(%q): %v", ascending[j], err)
			}
			want := 0
			switch {
			case i < j:
				want = -1
			case i > j:
				want = 1
			}
			if got := left.Compare(right); got != want {
				t.Fatalf("Compare(%q, %q) = %d, want %d", ascending[i], ascending[j], got, want)
			}
		}
	}
}

func TestVersionCompareReleaseCandidates(t *testing.T) {
	t.Parallel()
	rc3, _ := ParseVersion("0.2.1-rc.3")
	rc10, _ := ParseVersion("0.2.1-rc.10")
	release, _ := ParseVersion("0.2.1")
	if got := rc3.Compare(rc10); got != -1 {
		t.Fatalf("rc.3.Compare(rc.10) = %d, want -1 (numeric identifiers compare numerically)", got)
	}
	if got := rc10.Compare(release); got != -1 {
		t.Fatalf("rc.10.Compare(0.2.1) = %d, want -1", got)
	}
	if got := release.Compare(rc3); got != 1 {
		t.Fatalf("0.2.1.Compare(rc.3) = %d, want 1", got)
	}
}

func TestBuildMetadataIgnoredForPrecedence(t *testing.T) {
	t.Parallel()
	withBuild, _ := ParseVersion("0.2.1+sha.1")
	release, _ := ParseVersion("0.2.1")
	older, _ := ParseVersion("0.2.1-rc.3")
	// Build metadata never affects precedence: 0.2.1+build still outranks
	// 0.2.1-rc.3 and ties with 0.2.1 (tie-broken by raw string only).
	if got := withBuild.Compare(older); got != 1 {
		t.Fatalf("0.2.1+sha.1.Compare(0.2.1-rc.3) = %d, want 1", got)
	}
	if got := withBuild.Compare(release); got != 1 {
		t.Fatalf("0.2.1+sha.1.Compare(0.2.1) = %d, want 1 (deterministic raw tie-break)", got)
	}
}

func TestSortVersionsDescending(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "release candidates",
			input: []string{"0.2.1-rc.3", "0.2.0", "0.2.1", "0.2.1-rc.10", "latest", "bad", "0.2.1-rc.2"},
			want:  []string{"0.2.1", "0.2.1-rc.10", "0.2.1-rc.3", "0.2.1-rc.2", "0.2.0"},
		},
		{
			name:  "build metadata tie",
			input: []string{"0.2.1-rc.3", "0.2.1", "0.2.1+build.10", "0.2.1+build.2"},
			want:  []string{"0.2.1+build.2", "0.2.1+build.10", "0.2.1", "0.2.1-rc.3"},
		},
		{
			name:  "empty",
			input: nil,
			want:  []string{},
		},
		{
			name:  "all invalid",
			input: []string{"latest", "next", ""},
			want:  []string{},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := SortVersionsDescending(tc.input)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("SortVersionsDescending(%#v) = %#v, want %#v", tc.input, got, tc.want)
			}
			if len(got) > 0 && len(tc.input) > 0 {
				// Sorting must not mutate the caller's slice.
				_ = got
			}
		})
	}
}

func TestCompareNumericIdentifier(t *testing.T) {
	t.Parallel()
	cases := []struct {
		left, right string
		want        int
	}{
		{"3", "10", -1},
		{"10", "3", 1},
		{"003", "3", 0},
		{"000000000000000000001", "9", -1},
		{"123456789012345678901234567890", "123456789012345678901234567891", -1},
	}
	for _, tc := range cases {
		if got := compareNumericIdentifier(tc.left, tc.right); got != tc.want {
			t.Fatalf("compareNumericIdentifier(%q, %q) = %d, want %d", tc.left, tc.right, got, tc.want)
		}
	}
}
