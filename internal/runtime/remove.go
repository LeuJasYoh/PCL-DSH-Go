// SPDX-License-Identifier: MIT

package runtime

import (
	"fmt"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
)

// Remove moves an installed runtime directory to <dataRoot>/trash and clears its
// capability probe cache (docs/M1_INTERFACES.md 8).
//
// A version that is not installed — invalid SemVer, missing directory, or a
// runtime whose CLI entry no longer validates — yields a 404 *apierr.Error so the
// API layer can map it directly. Trash-move failures are returned wrapped for the
// caller to log/map as INTERNAL.
func (s *Service) Remove(version string) (string, error) {
	if !IsVersion(version) || !versionPathPattern.MatchString(version) {
		return "", apierr.NotFound(fmt.Sprintf("运行时 %q 不存在。", version))
	}
	directory, err := s.runtimeDirectory(version)
	if err != nil {
		return "", apierr.NotFound(fmt.Sprintf("运行时 %s 不存在。", version))
	}
	if _, err := validateExecutable(directory, version); err != nil {
		return "", apierr.NotFound(fmt.Sprintf("运行时 %s 尚未安装。", version))
	}
	trashPath, err := store.MoveToTrash(s.dataRoot, "runtime", directory)
	if err != nil {
		return "", fmt.Errorf("删除运行时 %s 失败：%w", version, err)
	}
	s.invalidateCapabilities(version)
	return trashPath, nil
}
