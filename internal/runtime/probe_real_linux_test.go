// SPDX-License-Identifier: MIT

//go:build linux

package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/config"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/tools"
)

// TestProbeRealDSHRuntime probes a real DSH runtime in WSL when the environment
// provides one. The runtime directory is only read (it is symlinked into a
// throw-away data root); probe cache directories are created inside a temp dir
// and removed by probeCapabilities.
//
// Environment:
//
//	PCL_DSH_REAL_NODE    absolute path to the bundled node executable
//	PCL_DSH_REAL_RUNTIME absolute path to runtimes/<version> (e.g.
//	                     /tmp/pcl-dsh-go-test/runtimes/0.2.1-rc.3)
//	PCL_DSH_REAL_TOOLS   optional tools root containing pnpm/ (enables the
//	                     BuildLaunchSpec assertion)
func TestProbeRealDSHRuntime(t *testing.T) {
	nodePath := os.Getenv("PCL_DSH_REAL_NODE")
	realRuntimeDirectory := os.Getenv("PCL_DSH_REAL_RUNTIME")
	if nodePath == "" || realRuntimeDirectory == "" {
		t.Skip("set PCL_DSH_REAL_NODE and PCL_DSH_REAL_RUNTIME to probe a real DSH runtime")
	}
	version := filepath.Base(filepath.Clean(realRuntimeDirectory))
	if !IsVersion(version) {
		t.Fatalf("runtime directory base %q is not an exact SemVer", version)
	}
	dataRoot := t.TempDir()
	runtimesDirectory := filepath.Join(dataRoot, "runtimes")
	if err := os.MkdirAll(runtimesDirectory, 0o700); err != nil {
		t.Fatalf("mkdir runtimes: %v", err)
	}
	// The real runtime stays read-only: expose it through a symlink.
	if err := os.Symlink(realRuntimeDirectory, filepath.Join(runtimesDirectory, version)); err != nil {
		t.Fatalf("symlink real runtime: %v", err)
	}
	toolRoot := os.Getenv("PCL_DSH_REAL_TOOLS")
	toolchain := &tools.Tools{
		NodePath:   nodePath,
		PnpmScript: filepath.Join(toolRoot, "pnpm", "node_modules", "pnpm", "bin", "pnpm.cjs"),
		ToolRoot:   toolRoot,
	}
	service := New(nil, toolchain, dataRoot)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	capabilities, err := service.Probe(ctx, version, "web")
	if err != nil {
		t.Fatalf("Probe(%s) error = %v", version, err)
	}
	t.Logf("real capabilities: launcher=%v app=%v plugin=%v webAlias=%v verified=%v entry=%s warnings=%v",
		capabilities.LauncherOptions, capabilities.AppOptions, capabilities.PluginManagement,
		capabilities.WebAlias, capabilities.AppHelpVerified(), capabilities.EntryPath, capabilities.Warnings())
	if !capabilities.ProfileSelection() {
		t.Fatalf("real DSH launcher does not declare --profile: %v", capabilities.LauncherOptions)
	}
	if !capabilities.Supports("--port") {
		t.Fatalf("real DSH web app does not declare --port: %v", capabilities.AppOptions)
	}
	if !capabilities.AppHelpVerified() {
		t.Fatalf("real DSH web app help was not verified")
	}
	if !capabilities.Supports("--host") || !capabilities.Supports("--no-open") {
		t.Logf("WARNING: real DSH web help is missing --host/--no-open; launch command will be trimmed")
	}
	if len(capabilities.Warnings()) != 0 {
		t.Logf("capability warnings: %v", capabilities.Warnings())
	}

	// BuildLaunchSpec must produce the documented argv with the created dirs.
	dirs := config.Dirs{
		DataRoot:     dataRoot,
		ConfigDir:    filepath.Join(dataRoot, "config"),
		InstancesDir: filepath.Join(dataRoot, "instances"),
		RuntimesDir:  runtimesDirectory,
		CacheDir:     filepath.Join(dataRoot, "cache"),
		LogsDir:      filepath.Join(dataRoot, "logs"),
		TrashDir:     filepath.Join(dataRoot, "trash"),
	}
	for _, directory := range []string{dirs.ConfigDir, dirs.InstancesDir, dirs.LogsDir, dirs.CacheDir, dirs.TrashDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", directory, err)
		}
	}
	instance := store.Instance{ID: "i-0123456789ab", Name: "真实运行时验证", Version: version, Profile: "web", Port: 39876}
	spec, err := service.BuildLaunchSpec(instance, dirs, toolchain, DefaultRegistry, "", capabilities)
	if err != nil {
		t.Fatalf("BuildLaunchSpec() error = %v", err)
	}
	joinedCommand := strings.Join(spec.Command, " ")
	t.Logf("real launch command: %s", joinedCommand)
	if !strings.Contains(joinedCommand, "--profile web --port 39876") {
		t.Fatalf("launch command = %v, want profile selector before app options", spec.Command)
	}
	for _, forbidden := range []string{"--patch", "--account", "--api-key", "--token"} {
		for _, argument := range spec.Command {
			if argument == forbidden {
				t.Fatalf("launch command injected %q: %v", forbidden, spec.Command)
			}
		}
	}
	if spec.Dir != filepath.Join(dirs.InstancesDir, instance.ID, "workspace") {
		t.Fatalf("launch dir = %q", spec.Dir)
	}
	for _, entry := range spec.Env {
		if strings.Contains(entry, "API_KEY") || strings.Contains(entry, "TOKEN=") {
			t.Fatalf("DSH launch env contains account credentials: %q", entry)
		}
	}
	envText := strings.Join(spec.Env, "\n")
	for _, expected := range []string{
		"HOME=" + spec.Dir,
		"DSH_HOME=",
		"DSH_AGENTS_HOME=",
		"PNPM_CONFIG_STORE_DIR=" + filepath.Join(dirs.CacheDir, "pnpm-store"),
		"DSH_TELEMETRY_MODE=DISABLED",
		"PATH=",
	} {
		if !strings.Contains(envText, expected) {
			t.Fatalf("launch env is missing %q: %v", expected, spec.Env)
		}
	}
	if got := service.Compatibility(version); got != m0CompatibilityLabel {
		t.Fatalf("Compatibility() = %q, want %q", got, m0CompatibilityLabel)
	}
}
