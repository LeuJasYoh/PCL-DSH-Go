// SPDX-License-Identifier: MIT

// Package passhash validates and hashes the single administrator password.
//
// New hashes use SHA-256 (base64) followed by BCrypt so the input stays below
// BCrypt's 72-byte limit while supporting long passwords. Legacy BCrypt hashes
// remain verifiable and are upgraded on the next password change.
package passhash

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

const (
	// MinRunes is the minimum password length in Unicode code points.
	MinRunes = 8
	// MaxRunes is the maximum password length in Unicode code points.
	MaxRunes = 256
	// BcryptCost is the BCrypt cost used for new hashes.
	BcryptCost = 10
	// HashPrefix identifies hashes that pre-hash the password with SHA-256.
	HashPrefix = "bcrypt-sha256:"
)

// Validate applies the administrator password policy.
func Validate(raw string) error {
	if raw == "" {
		return errors.New("新密码不能为空")
	}
	runes := utf8.RuneCountInString(raw)
	if runes < MinRunes || runes > MaxRunes {
		return fmt.Errorf("新密码长度须为 %d-%d 个字符（当前 %d）", MinRunes, MaxRunes, runes)
	}
	for _, r := range raw {
		if unicode.IsControl(r) {
			return errors.New("新密码不能包含换行或控制字符")
		}
	}
	return nil
}

// Hash returns a versioned password hash. The raw password is never stored.
func Hash(raw string) (string, error) {
	if err := Validate(raw); err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(raw))
	encoded := base64.StdEncoding.EncodeToString(digest[:])
	hash, err := bcrypt.GenerateFromPassword([]byte(encoded), BcryptCost)
	if err != nil {
		return "", err
	}
	return HashPrefix + string(hash), nil
}

// Verify checks raw against a versioned or legacy BCrypt hash.
func Verify(encoded, raw string) (bool, error) {
	if encoded == "" || raw == "" {
		return false, nil
	}
	hash := encoded
	secret := []byte(raw)
	if strings.HasPrefix(encoded, HashPrefix) {
		digest := sha256.Sum256([]byte(raw))
		hash = strings.TrimPrefix(encoded, HashPrefix)
		secret = []byte(base64.StdEncoding.EncodeToString(digest[:]))
	} else if len(secret) > 72 {
		// Legacy BCrypt hashes cannot represent longer inputs; treat as a
		// normal mismatch instead of surfacing an internal error.
		return false, nil
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), secret); err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ValidateHash reports whether encoded is a supported password hash.
func ValidateHash(encoded string) error {
	if encoded == "" {
		return errors.New("密码哈希为空")
	}
	hash := encoded
	if strings.HasPrefix(encoded, HashPrefix) {
		hash = strings.TrimPrefix(encoded, HashPrefix)
		if hash == "" {
			return errors.New("密码哈希为空")
		}
	}
	if _, err := bcrypt.Cost([]byte(hash)); err != nil {
		return err
	}
	return nil
}
