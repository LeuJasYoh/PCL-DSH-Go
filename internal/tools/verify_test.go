// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseVersion(t *testing.T) {
	cases := []struct {
		name    string
		output  string
		want    string
		wantErr bool
	}{
		{name: "node 输出", output: "v24.22.1\n", want: "24.22.1"},
		{name: "pnpm 输出", output: "11.28.5\n", want: "11.28.5"},
		{name: "无前缀", output: "1.2.3", want: "1.2.3"},
		{name: "预发布版本", output: "v0.2.1-rc.3", want: "0.2.1-rc.3"},
		{name: "带构建元数据", output: "1.2.3+build.5", want: "1.2.3+build.5"},
		{name: "多行取首行", output: "v24.22.1\nwarning: something\n", want: "24.22.1"},
		{name: "空输出", output: "  \n", wantErr: true},
		{name: "垃圾输出", output: "not-a-version", wantErr: true},
		{name: "不完整版本", output: "v24.22", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseVersion(tc.output)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseVersion(%q) 期望报错，实际 %q", tc.output, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseVersion(%q) 失败：%v", tc.output, err)
			}
			if got != tc.want {
				t.Errorf("parseVersion(%q) = %q, 期望 %q", tc.output, got, tc.want)
			}
		})
	}
}

func TestVerifyMissingExecutable(t *testing.T) {
	dir := t.TempDir()
	tl := &Tools{
		NodePath:   filepath.Join(dir, "missing-node"),
		PnpmScript: filepath.Join(dir, "pnpm.cjs"),
	}
	err := tl.Verify(context.Background())
	if err == nil {
		t.Fatal("node 不存在时 Verify 应失败")
	}
	if !strings.Contains(err.Error(), "node -v") {
		t.Errorf("错误 %q 未指明 node -v", err.Error())
	}
	if tl.NodeVersion != "" || tl.PnpmVersion != "" {
		t.Errorf("失败时不应写入版本：node=%q pnpm=%q", tl.NodeVersion, tl.PnpmVersion)
	}
}

func TestVerifyIncompleteTools(t *testing.T) {
	if err := (&Tools{}).Verify(context.Background()); err == nil {
		t.Fatal("空 Tools 应报错")
	}
	if err := (&Tools{NodePath: "/x"}).Verify(context.Background()); err == nil {
		t.Fatal("缺 pnpm 应报错")
	}
	var nilTools *Tools
	if err := nilTools.Verify(context.Background()); err == nil {
		t.Fatal("nil Tools 应报错")
	}
}
