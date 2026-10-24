// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

// Entry is one installed runtime as listed by Entries (docs/M1_INTERFACES.md 8).
type Entry struct {
	// Version is the exact installed SemVer version.
	Version string
	// Compatibility is "verified" after a successful web-profile probe and
	// "unverified" otherwise (the M1 compatibility matrix is not implemented
	// yet, so installed runtimes default to "unverified" until probed).
	Compatibility string
	// EntryPath is the absolute path of the validated dsh CLI entry.
	EntryPath string
}

// Entries returns the installed runtimes whose CLI entry validates, sorted by
// descending SemVer precedence. Unreadable or incomplete runtime directories are
// ignored (they are not usable runtimes).
func (s *Service) Entries() []Entry {
	versions := s.InstalledVersions()
	entries := make([]Entry, 0, len(versions))
	for _, version := range versions {
		entryPath, err := s.executable(version)
		if err != nil {
			continue
		}
		entries = append(entries, Entry{
			Version:       version,
			Compatibility: s.Compatibility(version),
			EntryPath:     entryPath,
		})
	}
	return entries
}

// CompatibilityOf reports "verified" or "unverified" for an installed runtime.
// It reuses the capability probe cache populated by Probe.
func (s *Service) CompatibilityOf(version string) string {
	return s.Compatibility(version)
}
