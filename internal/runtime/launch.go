// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/config"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/process"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/tools"
)

// BuildLaunchSpec lives in this file so launch planning is easy to review
// against docs/DSH_INTEGRATION.md 1.2/4.2.
// BuildLaunchSpec builds the process.LaunchSpec for one instance
// (docs/DSH_INTEGRATION.md 1.2 and 4.2), creating the instance directories.
func (s *Service) BuildLaunchSpec(instance store.Instance, dirs config.Dirs, tl *tools.Tools, registry, proxy string, capabilities Capabilities) (process.LaunchSpec, error) {
	if tl == nil || tl.NodePath == "" {
		return process.LaunchSpec{}, fmt.Errorf("构建启动命令失败：未定位到 Node.js 工具链")
	}
	if strings.TrimSpace(instance.ID) == "" {
		return process.LaunchSpec{}, fmt.Errorf("构建启动命令失败：实例 ID 为空")
	}
	if !IsVersion(instance.Version) {
		return process.LaunchSpec{}, fmt.Errorf("构建启动命令失败：实例版本 %q 不是完整 SemVer", instance.Version)
	}
	if !validProfileName(instance.Profile) {
		return process.LaunchSpec{}, fmt.Errorf("构建启动命令失败：profile 名称无效：%q", instance.Profile)
	}
	if capabilities.Version != "" && capabilities.Version != instance.Version {
		return process.LaunchSpec{}, fmt.Errorf("构建启动命令失败：能力探测版本 %q 与实例版本 %q 不符", capabilities.Version, instance.Version)
	}
	if capabilities.Profile != "" && capabilities.Profile != instance.Profile {
		return process.LaunchSpec{}, fmt.Errorf("构建启动命令失败：能力探测 profile %q 与实例 profile %q 不符", capabilities.Profile, instance.Profile)
	}
	if dirs.RuntimesDir == "" || dirs.InstancesDir == "" || dirs.LogsDir == "" || dirs.DataRoot == "" {
		return process.LaunchSpec{}, fmt.Errorf("构建启动命令失败：数据目录不完整")
	}
	entry, err := validateExecutable(filepath.Join(dirs.RuntimesDir, instance.Version), instance.Version)
	if err != nil {
		return process.LaunchSpec{}, fmt.Errorf("构建启动命令失败：运行时 %s 不可用：%w", instance.Version, err)
	}
	command, err := assembleLaunchCommand(tl.NodePath, entry, capabilities, instance.Profile, instance.Port)
	if err != nil {
		return process.LaunchSpec{}, err
	}
	instanceDirectory := filepath.Join(dirs.InstancesDir, instance.ID)
	dshHome := filepath.Join(instanceDirectory, "dsh-home")
	agentsHome := filepath.Join(instanceDirectory, "agents-home")
	workspace := filepath.Join(instanceDirectory, "workspace")
	shimsDirectory := filepath.Join(dirs.DataRoot, "tools", "shims")
	for _, directory := range []string{dshHome, agentsHome, workspace, shimsDirectory} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return process.LaunchSpec{}, fmt.Errorf("创建实例目录失败：%w", err)
		}
	}
	environment, err := buildLaunchEnvironment(launchEnvOptions{
		DataRoot:       dirs.DataRoot,
		NodePath:       tl.NodePath,
		PnpmHome:       derivePnpmHome(tl.ToolRoot, dirs.DataRoot),
		ShimsDirectory: shimsDirectory,
		DSHHome:        dshHome,
		AgentsHome:     agentsHome,
		Registry:       registry,
		Proxy:          proxy,
	})
	if err != nil {
		return process.LaunchSpec{}, fmt.Errorf("构建启动环境失败：%w", err)
	}
	return process.LaunchSpec{
		InstanceID: instance.ID,
		Command:    command,
		Env:        environment,
		Dir:        workspace,
		Port:       instance.Port,
		LogPath:    filepath.Join(dirs.LogsDir, "instance-"+instance.ID+".log"),
	}, nil
}
