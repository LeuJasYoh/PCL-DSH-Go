// SPDX-License-Identifier: MIT

package logging

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// DefaultTailLines 是日志 tail 的默认行数。
	DefaultTailLines = 800
	// MaxTailLines 是日志 tail 允许的最大行数。
	MaxTailLines  = 2000
	tailChunkSize = 32 * 1024
)

// managerLogNameRE 匹配按日切分的 manager 日志文件名。
var managerLogNameRE = regexp.MustCompile(`^manager-(\d{4}-\d{2}-\d{2})\.log$`)

// TailLatest 读取 logsDir 中最新的 manager-YYYY-MM-DD.log 的最后 lines 行。
// lines 为 0/负数时使用默认 800，超过 2000 时截断为 2000。
// 每行都经过 Redact 脱敏后返回；日志文件不存在时返回空切片与错误。
func TailLatest(logsDir string, lines int) ([]string, error) {
	dir := strings.TrimSpace(logsDir)
	if dir == "" {
		return []string{}, errors.New("日志目录不能为空")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return []string{}, fmt.Errorf("解析日志目录绝对路径失败：%w", err)
	}
	limit := normalizeTailLines(lines)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{}, fmt.Errorf("读取日志目录 %s 失败：%w", dir, err)
	}
	latestDate := ""
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := managerLogNameRE.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		if match[1] > latestDate {
			latestDate = match[1]
		}
	}
	if latestDate == "" {
		return []string{}, fmt.Errorf("未找到 manager 日志文件（目录 %s）", dir)
	}
	path := filepath.Join(dir, "manager-"+latestDate+".log")
	rawLines, err := tailFileLines(path, limit)
	if err != nil {
		return []string{}, err
	}
	out := make([]string, len(rawLines))
	for i, line := range rawLines {
		out[i] = Redact(line)
	}
	return out, nil
}

// normalizeTailLines 把请求行数收敛到 1-2000；0 或负数回退默认值。
func normalizeTailLines(lines int) int {
	if lines <= 0 {
		return DefaultTailLines
	}
	if lines > MaxTailLines {
		return MaxTailLines
	}
	return lines
}

// tailFileLines 从文件尾部最多读取 limit 行，避免大日志整文件载入内存。
func tailFileLines(path string, limit int) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开日志文件 %s 失败：%w", path, err)
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("读取日志文件信息 %s 失败：%w", path, err)
	}
	size := info.Size()
	if size <= 0 {
		return []string{}, nil
	}

	newlines := 0
	offset := size
	var data []byte
	for offset > 0 && newlines < limit+1 {
		readSize := int64(tailChunkSize)
		if readSize > offset {
			readSize = offset
		}
		offset -= readSize
		chunk := make([]byte, readSize)
		if _, err := file.ReadAt(chunk, offset); err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("读取日志文件 %s 失败：%w", path, err)
		}
		data = append(chunk, data...)
		newlines += bytes.Count(chunk, []byte{'\n'})
	}

	lines := strings.Split(string(data), "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	// 从文件中间开始读取时，首行可能是被截断的半行，丢弃。
	if offset > 0 && len(lines) > 0 {
		lines = lines[1:]
	}
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines, nil
}
