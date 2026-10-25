// SPDX-License-Identifier: GPL-3.0-or-later

// Package profile validates DSH profile names used as directories under
// $DSH_HOME/profiles and as --profile command-line arguments.
//
// DSH itself accepts Unicode and spaces, but rejects only empty names, path
// separators, ".", ".." and "node_modules". The manager is stricter: no control
// characters, no leading/trailing whitespace, no consecutive dots, no trailing
// dot, no Windows reserved names, and the first character must be a letter or
// number. Names are limited to 64 Unicode code points.
package profile

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxRunes is the maximum profile name length in Unicode code points.
	MaxRunes = 64
)

// reservedNames are rejected for path portability and DSH compatibility.
var reservedNames = map[string]struct{}{
	"node_modules": {},
	"con":          {},
	"prn":          {},
	"aux":          {},
	"nul":          {},
	"com1":         {},
	"com2":         {},
	"com3":         {},
	"com4":         {},
	"com5":         {},
	"com6":         {},
	"com7":         {},
	"com8":         {},
	"com9":         {},
	"lpt1":         {},
	"lpt2":         {},
	"lpt3":         {},
	"lpt4":         {},
	"lpt5":         {},
	"lpt6":         {},
	"lpt7":         {},
	"lpt8":         {},
	"lpt9":         {},
}

// Validate reports whether raw is a safe DSH profile name.
func Validate(raw string) error {
	if raw == "" {
		return errors.New("profile 不能为空（例如 web）")
	}
	if raw != strings.TrimSpace(raw) {
		return errors.New("profile 首尾不能有空格")
	}
	if utf8.RuneCountInString(raw) > MaxRunes {
		return fmt.Errorf("profile 名称不能超过 %d 个字符", MaxRunes)
	}
	if strings.Contains(raw, "/") || strings.Contains(raw, "\\") {
		return errors.New("profile 不能包含路径分隔符")
	}
	if strings.Contains(raw, "..") {
		return errors.New("profile 不能包含连续点")
	}
	if strings.HasSuffix(raw, ".") {
		return errors.New("profile 不能以点结尾")
	}
	if isReservedName(raw) {
		return fmt.Errorf("profile 不能使用系统保留名 %q", raw)
	}
	for index, r := range raw {
		if unicode.IsControl(r) {
			return errors.New("profile 不能包含控制字符")
		}
		if index == 0 && !unicode.IsLetter(r) && !unicode.IsNumber(r) {
			return errors.New("profile 必须以字母或数字开头")
		}
		if r == ' ' || r == '.' || r == '_' || r == '-' {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			continue
		}
		return fmt.Errorf("profile 含不允许字符 %q；只允许字母、数字、空格、点、下划线和连字符", r)
	}
	return nil
}

// Valid reports whether raw is a safe DSH profile name.
func Valid(raw string) bool {
	return Validate(raw) == nil
}

func isReservedName(raw string) bool {
	lowered := strings.ToLower(raw)
	if _, ok := reservedNames[lowered]; ok {
		return true
	}
	if index := strings.IndexByte(lowered, '.'); index > 0 {
		if _, ok := reservedNames[lowered[:index]]; ok {
			return true
		}
	}
	return false
}
