// SPDX-License-Identifier: MIT

package runtime

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// randomHex returns n cryptographically random bytes encoded as lowercase hex.
// It is used for staging/probe directory names that must never collide.
func randomHex(n int) (string, error) {
	buffer := make([]byte, n)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("生成随机标识失败：%w", err)
	}
	return hex.EncodeToString(buffer), nil
}
