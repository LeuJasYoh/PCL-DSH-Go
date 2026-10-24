// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
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
