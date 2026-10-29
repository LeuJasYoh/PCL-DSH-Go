// SPDX-License-Identifier: MIT

package profile

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	valid := []string{
		"web",
		"headless",
		"my-profile",
		"p-1_2.3",
		"web profile",
		"中文 配置",
		"网页",
		"профиль-1",
		"a.b_c-d",
		strings.Repeat("a", MaxRunes),
		strings.Repeat("密", MaxRunes),
	}
	for _, name := range valid {
		t.Run("valid/"+name, func(t *testing.T) {
			if err := Validate(name); err != nil {
				t.Fatalf("Validate(%q) error = %v, want nil", name, err)
			}
			if !Valid(name) {
				t.Fatalf("Valid(%q) = false, want true", name)
			}
		})
	}

	invalid := []struct {
		name string
		raw  string
	}{
		{"空", ""},
		{"首尾空格", " web"},
		{"尾部空格", "web "},
		{"全是空格", "   "},
		{"斜杠", "web/x"},
		{"反斜杠", `web\x`},
		{"连续点", "a..b"},
		{"结尾点", "a."},
		{"点开头", ".hidden"},
		{"下划线开头", "_web"},
		{"连字符开头", "-web"},
		{"控制字符", "a\nb"},
		{"冒号", "web:ui"},
		{"node_modules", "node_modules"},
		{"node_modules 大写", "Node_Modules"},
		{"CON", "CON"},
		{"aux 扩展名", "aux.txt"},
		{"超长", strings.Repeat("a", MaxRunes+1)},
	}
	for _, tc := range invalid {
		t.Run("invalid/"+tc.name, func(t *testing.T) {
			if err := Validate(tc.raw); err == nil {
				t.Fatalf("Validate(%q) error = nil, want an error", tc.raw)
			}
			if Valid(tc.raw) {
				t.Fatalf("Valid(%q) = true, want false", tc.raw)
			}
		})
	}
}

func TestValidateAllowsInternalSpace(t *testing.T) {
	if err := Validate("中文 配置"); err != nil {
		t.Fatalf("Validate(中文 配置) error = %v, want nil", err)
	}
}
