// SPDX-License-Identifier: GPL-3.0-or-later

package ports

import "testing"

func TestValidInstance(t *testing.T) {
	cases := []struct {
		port int
		want bool
	}{
		{1023, false},
		{1024, true},
		{3080, true},
		{65535, true},
		{65536, false},
	}
	for _, tc := range cases {
		if got := ValidInstance(tc.port); got != tc.want {
			t.Errorf("ValidInstance(%d) = %v, want %v", tc.port, got, tc.want)
		}
	}
}
