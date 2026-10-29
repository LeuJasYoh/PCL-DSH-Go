// SPDX-License-Identifier: MIT

package passhash

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		ok   bool
	}{
		{"空", "", false},
		{"7 字符", "short7x", false},
		{"8 字符", "12345678", true},
		{"256 字符", strings.Repeat("a", 256), true},
		{"257 字符", strings.Repeat("a", 257), false},
		{"控制字符", "line\nbreak", false},
		{"中文 256 字符", strings.Repeat("密", 256), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.raw)
			if tc.ok && err != nil {
				t.Fatalf("Validate() error = %v, want nil", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("Validate() error = nil, want an error")
			}
		})
	}
}

func TestHashAndVerify(t *testing.T) {
	passwords := []string{
		"12345678",
		strings.Repeat("a", 256),
		"中文密码-12345678",
	}
	for _, raw := range passwords {
		hash, err := Hash(raw)
		if err != nil {
			t.Fatalf("Hash(%q) error = %v", raw, err)
		}
		if !strings.HasPrefix(hash, HashPrefix) {
			t.Fatalf("Hash() = %q, want prefix %q", hash, HashPrefix)
		}
		if err := ValidateHash(hash); err != nil {
			t.Fatalf("ValidateHash() error = %v", err)
		}
		if ok, err := Verify(hash, raw); err != nil || !ok {
			t.Fatalf("Verify(%q) = %v, %v; want true, nil", raw, ok, err)
		}
		if ok, err := Verify(hash, raw+"x"); err != nil || ok {
			t.Fatalf("Verify(wrong) = %v, %v; want false, nil", ok, err)
		}
	}
}

func TestVerifyLegacyBcrypt(t *testing.T) {
	const raw = "legacy-password-1"
	legacy, err := bcrypt.GenerateFromPassword([]byte(raw), BcryptCost)
	if err != nil {
		t.Fatalf("bcrypt.GenerateFromPassword() error = %v", err)
	}
	if err := ValidateHash(string(legacy)); err != nil {
		t.Fatalf("ValidateHash(legacy) error = %v", err)
	}
	if ok, err := Verify(string(legacy), raw); err != nil || !ok {
		t.Fatalf("Verify(legacy) = %v, %v; want true, nil", ok, err)
	}
	if ok, err := Verify(string(legacy), "wrong-password"); err != nil || ok {
		t.Fatalf("Verify(legacy wrong) = %v, %v; want false, nil", ok, err)
	}
}

func TestValidateHashRejectsInvalid(t *testing.T) {
	for _, encoded := range []string{"", "not-a-hash", HashPrefix} {
		if err := ValidateHash(encoded); err == nil {
			t.Fatalf("ValidateHash(%q) error = nil, want an error", encoded)
		}
	}
}
