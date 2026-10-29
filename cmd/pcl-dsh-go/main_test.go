// SPDX-License-Identifier: MIT

package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// TestRunRejectsInvalidArgs verifies the CLI contract: invalid flags exit 2
// with usage text, --help exits 0 without starting the server.
func TestRunRejectsInvalidArgs(t *testing.T) {
	cases := [][]string{
		{"--server.port=0"},
		{"--unknown-flag"},
	}
	for _, args := range cases {
		if code := run(args); code != 2 {
			t.Fatalf("run(%v) = %d, want 2", args, code)
		}
	}
}

func TestRunHelp(t *testing.T) {
	if code := run([]string{"--help"}); code != 0 {
		t.Fatalf("run(--help) = %d, want 0", code)
	}
}

func TestRunVersion(t *testing.T) {
	previousVersion := version
	version = "test-version"
	defer func() { version = previousVersion }()

	originalStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("创建 stdout 管道失败：%v", err)
	}
	os.Stdout = writer
	code := run([]string{"--version"})
	_ = writer.Close()
	os.Stdout = originalStdout
	output, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if readErr != nil {
		t.Fatalf("读取 stdout 失败：%v", readErr)
	}
	if code != 0 {
		t.Fatalf("run(--version) = %d, want 0", code)
	}
	if got := strings.TrimSpace(string(output)); got != "test-version" {
		t.Fatalf("run(--version) 输出 = %q, want %q", got, "test-version")
	}
}
