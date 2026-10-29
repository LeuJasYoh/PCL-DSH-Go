// SPDX-License-Identifier: MIT

package runtime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/config"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/process"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/tools"
	"gopkg.in/yaml.v3"
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
	if err := ensureWorkspaceConfiguration(dshHome, workspace); err != nil {
		return process.LaunchSpec{}, fmt.Errorf("配置实例工作区失败：%w", err)
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
		Home:           workspace,
		HostPath:       os.Getenv("PATH"),
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

// Headless Linux hosts need an explicit Documents directory for first-use creation.
func ensureWorkspaceConfiguration(dshHome, workspace string) error {
	path := filepath.Join(dshHome, "cordis.patch.yml")
	document := &yaml.Node{}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(raw, document); err != nil {
			return fmt.Errorf("解析 %s：%w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
		document.Kind = yaml.DocumentNode
		document.Content = []*yaml.Node{{Kind: yaml.SequenceNode}}
	default:
		return fmt.Errorf("读取 %s：%w", path, err)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 || document.Content[0].Kind != yaml.SequenceNode {
		return fmt.Errorf("%s 顶层必须是 Cordis 补丁数组", path)
	}

	entries := document.Content[0]
	for _, entry := range entries.Content {
		if entry.Kind != yaml.MappingNode {
			continue
		}
		id := yamlMappingValue(entry, "id")
		if id == nil || id.Value != "workspace-controller" {
			continue
		}
		config := yamlMappingValue(entry, "config")
		if config == nil {
			config = &yaml.Node{Kind: yaml.MappingNode}
			entry.Content = append(entry.Content, yamlScalar("config"), config)
		}
		if config.Kind != yaml.MappingNode {
			return fmt.Errorf("%s 中 workspace-controller.config 必须是映射", path)
		}
		if yamlMappingValue(config, "documentsDirectory") != nil {
			return nil
		}
		config.Content = append(config.Content, yamlScalar("documentsDirectory"), yamlScalar(workspace))
		return writeWorkspaceConfiguration(path, document)
	}

	config := &yaml.Node{Kind: yaml.MappingNode}
	config.Content = []*yaml.Node{yamlScalar("documentsDirectory"), yamlScalar(workspace)}
	entry := &yaml.Node{Kind: yaml.MappingNode}
	entry.Content = []*yaml.Node{
		yamlScalar("id"), yamlScalar("workspace-controller"),
		yamlScalar("config"), config,
	}
	entries.Content = append(entries.Content, entry)
	return writeWorkspaceConfiguration(path, document)
}

func yamlMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1]
		}
	}
	return nil
}

func yamlScalar(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func writeWorkspaceConfiguration(path string, document *yaml.Node) error {
	data, err := yaml.Marshal(document)
	if err != nil {
		return fmt.Errorf("生成工作区配置：%w", err)
	}
	if err := writeFileAtomically(path, data, 0o600); err != nil {
		return fmt.Errorf("写入 %s：%w", path, err)
	}
	return nil
}
