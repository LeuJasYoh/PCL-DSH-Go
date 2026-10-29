// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// cacheSubdirs 是 data-root/cache 下必须存在的子目录（顺序固定，便于诊断）。
var cacheSubdirs = []string{"pnpm-store", "npm", "registry-metadata", "probes", "uploads", "exports", "pack-staging"}

// Dirs 是 data-root 的标准目录集合（绝对路径）。
type Dirs struct {
	DataRoot     string
	ConfigDir    string
	InstancesDir string
	RuntimesDir  string
	CacheDir     string
	LogsDir      string
	TrashDir     string
}

// InitDataRoot 创建 data-root 标准目录结构（权限 0700），并返回绝对路径。
// data-root 已存在时校验必须是目录；已存在目录的权限不做收紧（避免误改运维配置）。
func InitDataRoot(c Config) (Dirs, error) {
	root := strings.TrimSpace(c.DataRoot)
	if root == "" {
		return Dirs{}, errors.New("data-root 不能为空")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return Dirs{}, fmt.Errorf("解析 data-root 绝对路径失败：%w", err)
	}
	if stat, statErr := os.Stat(root); statErr == nil {
		if !stat.IsDir() {
			return Dirs{}, fmt.Errorf("data-root 已存在但不是目录：%s", root)
		}
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return Dirs{}, fmt.Errorf("检查 data-root %s 失败：%w", root, statErr)
	}

	dirs := Dirs{
		DataRoot:     root,
		ConfigDir:    filepath.Join(root, "config"),
		InstancesDir: filepath.Join(root, "instances"),
		RuntimesDir:  filepath.Join(root, "runtimes"),
		CacheDir:     filepath.Join(root, "cache"),
		LogsDir:      filepath.Join(root, "logs"),
		TrashDir:     filepath.Join(root, "trash"),
	}
	toCreate := []string{
		dirs.DataRoot,
		dirs.ConfigDir,
		dirs.InstancesDir,
		dirs.RuntimesDir,
		dirs.CacheDir,
		dirs.LogsDir,
		dirs.TrashDir,
	}
	for _, sub := range cacheSubdirs {
		toCreate = append(toCreate, filepath.Join(dirs.CacheDir, sub))
	}
	for _, dir := range toCreate {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return Dirs{}, fmt.Errorf("创建目录 %s 失败：%w", dir, err)
		}
	}
	return dirs, nil
}
