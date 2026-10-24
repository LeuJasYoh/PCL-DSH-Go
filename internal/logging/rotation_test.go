// SPDX-License-Identifier: GPL-3.0-or-later

package logging

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDailyFileRotatesByDate(t *testing.T) {
	dir := t.TempDir()
	current := time.Date(2026, 10, 24, 23, 59, 0, 0, time.Local)
	clock := func() time.Time { return current }
	w, err := newDailyFile(dir, "manager", clock)
	if err != nil {
		t.Fatalf("newDailyFile 失败：%v", err)
	}
	if _, err := w.Write([]byte("day1\n")); err != nil {
		t.Fatalf("写入 day1 失败：%v", err)
	}
	current = current.Add(2 * time.Minute) // 跨到 2026-10-25
	if _, err := w.Write([]byte("day2\n")); err != nil {
		t.Fatalf("写入 day2 失败：%v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close 失败：%v", err)
	}

	first := filepath.Join(dir, "manager-2026-10-24.log")
	second := filepath.Join(dir, "manager-2026-10-25.log")
	dataFirst, err := os.ReadFile(first)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", first, err)
	}
	dataSecond, err := os.ReadFile(second)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", second, err)
	}
	if string(dataFirst) != "day1\n" {
		t.Errorf("%s 内容 = %q", first, string(dataFirst))
	}
	if string(dataSecond) != "day2\n" {
		t.Errorf("%s 内容 = %q", second, string(dataSecond))
	}
}

func TestDailyFileConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	w, err := newDailyFile(dir, "manager", time.Now)
	if err != nil {
		t.Fatalf("newDailyFile 失败：%v", err)
	}
	const writers = 8
	const linesPerWriter = 25
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < linesPerWriter; j++ {
				if _, err := w.Write([]byte("line\n")); err != nil {
					t.Errorf("并发写入失败：%v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if err := w.Close(); err != nil {
		t.Fatalf("Close 失败：%v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, w.fileName(time.Now().Format(dateLayout))))
	if err != nil {
		t.Fatalf("读取日志失败：%v", err)
	}
	if got := strings.Count(string(data), "line\n"); got != writers*linesPerWriter {
		t.Errorf("日志行数 = %d, 期望 %d", got, writers*linesPerWriter)
	}
}
