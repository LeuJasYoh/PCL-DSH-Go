// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package safe

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestZipExtractRejectsSymlinkParentChain 验证解包目标父链含符号链接时拒绝。
func TestZipExtractRejectsSymlinkParentChain(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(realDir, 0o700); err != nil {
		t.Fatalf("创建真实目录失败: %v", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatalf("创建符号链接失败: %v", err)
	}
	zipPath := buildZip(t, []zipSample{{name: "a.txt", data: []byte("hello")}})
	archive, err := OpenZip(zipPath, Limits{})
	if err != nil {
		t.Fatalf("OpenZip 失败: %v", err)
	}
	defer archive.Close()
	assertKind(t, archive.Extract(context.Background(), filepath.Join(link, "out")), ErrUnsafePath)
	entries, err := os.ReadDir(realDir)
	if err != nil {
		t.Fatalf("读取真实目录失败: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("拒绝后不应在真实目录写入文件: %v", entries)
	}
}

// TestZipExtractRejectsSymlinkDestination 验证解包目标本身是符号链接时拒绝。
func TestZipExtractRejectsSymlinkDestination(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "target")
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		t.Fatalf("创建目标目录失败: %v", err)
	}
	link := filepath.Join(root, "dest-link")
	if err := os.Symlink(targetDir, link); err != nil {
		t.Fatalf("创建符号链接失败: %v", err)
	}
	zipPath := buildZip(t, []zipSample{{name: "a.txt", data: []byte("hello")}})
	archive, err := OpenZip(zipPath, Limits{})
	if err != nil {
		t.Fatalf("OpenZip 失败: %v", err)
	}
	defer archive.Close()
	assertKind(t, archive.Extract(context.Background(), link), ErrUnsafePath)
}

// TestTarExtractRejectsSymlinkParentChain 验证 tar.gz 解包的父链符号链接检查。
func TestTarExtractRejectsSymlinkParentChain(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(realDir, 0o700); err != nil {
		t.Fatalf("创建真实目录失败: %v", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatalf("创建符号链接失败: %v", err)
	}
	tarPath := buildTarGz(t, []tarSample{{name: "a.txt", data: []byte("hello")}})
	archive, err := OpenTarGz(tarPath, Limits{})
	if err != nil {
		t.Fatalf("OpenTarGz 失败: %v", err)
	}
	defer archive.Close()
	assertKind(t, archive.Extract(context.Background(), filepath.Join(link, "out")), ErrUnsafePath)
	entries, err := os.ReadDir(realDir)
	if err != nil {
		t.Fatalf("读取真实目录失败: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("拒绝后不应在真实目录写入文件: %v", entries)
	}
}

// TestOpenArchiveRejectsSymlinkPath 验证归档文件本身是符号链接时拒绝。
func TestOpenArchiveRejectsSymlinkPath(t *testing.T) {
	root := t.TempDir()
	zipPath := buildZip(t, []zipSample{{name: "a.txt", data: []byte("hello")}})
	zipLink := filepath.Join(root, "link.zip")
	if err := os.Symlink(zipPath, zipLink); err != nil {
		t.Fatalf("创建符号链接失败: %v", err)
	}
	archive, err := OpenZip(zipLink, Limits{})
	if archive != nil {
		_ = archive.Close()
	}
	assertKind(t, err, ErrUnsupported)

	tarPath := buildTarGz(t, []tarSample{{name: "a.txt", data: []byte("hello")}})
	tarLink := filepath.Join(root, "link.tar.gz")
	if err := os.Symlink(tarPath, tarLink); err != nil {
		t.Fatalf("创建符号链接失败: %v", err)
	}
	tarArchive, tarErr := OpenTarGz(tarLink, Limits{})
	if tarArchive != nil {
		_ = tarArchive.Close()
	}
	assertKind(t, tarErr, ErrUnsupported)
}
