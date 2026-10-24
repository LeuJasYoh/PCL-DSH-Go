// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"strings"
	"testing"
)

func TestMaxRunningInstancesConfig(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
	}{
		{name: "默认 1", args: nil, want: DefaultMaxRunningInstances},
		{name: "显式 1", args: []string{"--pcl.max-running-instances=1"}, want: 1},
		{name: "可调大", args: []string{"--pcl.max-running-instances=3"}, want: 3},
		{name: "0 表示不限制", args: []string{"--pcl.max-running-instances=0"}, want: 0},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			cfg, err := Load(testCase.args, func(string) string { return "" })
			if err != nil {
				t.Fatalf("Load(%v) 失败: %v", testCase.args, err)
			}
			if cfg.MaxRunningInstances != testCase.want {
				t.Fatalf("MaxRunningInstances = %d，期望 %d", cfg.MaxRunningInstances, testCase.want)
			}
		})
	}
}

func TestMaxRunningInstancesRejectsNegative(t *testing.T) {
	_, err := Load([]string{"--pcl.max-running-instances=-1"}, func(string) string { return "" })
	if err == nil {
		t.Fatal("负数上限应报错")
	}
	if !strings.Contains(err.Error(), "不能为负数") || !strings.Contains(err.Error(), "--pcl.max-running-instances") {
		t.Fatalf("错误信息不可操作: %v", err)
	}
}

func TestUsageTextMentionsMaxRunningInstances(t *testing.T) {
	text := UsageText()
	if !strings.Contains(text, "--pcl.max-running-instances") {
		t.Fatalf("UsageText 未列出 --pcl.max-running-instances:\n%s", text)
	}
	if !strings.Contains(text, "0=不限制") {
		t.Fatalf("UsageText 未说明 0=不限制:\n%s", text)
	}
}
