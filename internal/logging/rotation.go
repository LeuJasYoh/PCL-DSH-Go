// SPDX-License-Identifier: MIT

package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// dailyFile 是按本地日期切换的追加日志文件，线程安全。
type dailyFile struct {
	mu     sync.Mutex
	dir    string
	prefix string
	now    func() time.Time
	file   *os.File
	date   string
}

// newDailyFile 打开（必要时创建）当日日志文件。
func newDailyFile(dir, prefix string, now func() time.Time) (*dailyFile, error) {
	if now == nil {
		now = time.Now
	}
	w := &dailyFile{dir: dir, prefix: prefix, now: now}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.openLocked(now().Format(dateLayout)); err != nil {
		return nil, err
	}
	return w, nil
}

// fileName 返回指定日期的日志文件名。
func (w *dailyFile) fileName(date string) string {
	return fmt.Sprintf("%s-%s.log", w.prefix, date)
}

// openLocked 切换到指定日期的日志文件；调用方必须持有 w.mu。
func (w *dailyFile) openLocked(date string) error {
	path := filepath.Join(w.dir, w.fileName(date))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("打开日志文件 %s 失败：%w", path, err)
	}
	if w.file != nil {
		_ = w.file.Close()
	}
	w.file = f
	w.date = date
	return nil
}

// Write 实现 io.Writer；跨日时自动切换到新文件。
func (w *dailyFile) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, os.ErrClosed
	}
	date := w.now().Format(dateLayout)
	if w.date != date {
		if err := w.openLocked(date); err != nil {
			return 0, err
		}
	}
	return w.file.Write(p)
}

// Close 关闭当前日志文件；可重复调用。
func (w *dailyFile) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	if err != nil {
		return fmt.Errorf("关闭日志文件失败：%w", err)
	}
	return nil
}
