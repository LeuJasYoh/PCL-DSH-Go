// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/logging"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
)

// UpdateSettings validates and persists manager settings, then refreshes the
// runtime service proxy so the next registry/install call uses it.
func (a *App) UpdateSettings(ctx context.Context, settings store.Settings) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !a.tryLock() {
		return apierr.Busy()
	}
	defer a.unlock()

	if err := a.store.UpdateSettings(settings); err != nil {
		if errors.Is(err, store.ErrInvalidInput) {
			return validationError(err.Error())
		}
		a.logger.Error("保存设置失败", "error", err)
		return internalError("保存设置失败，请稍后重试。")
	}
	current := a.store.Settings()
	a.runtime.SetProxy(current.Proxy)
	a.logger.Info("设置已更新",
		"registry", current.Registry, "proxy", current.Proxy, "sshUser", current.DirectAccessSSHUser)
	return nil
}

// LogsTail returns the last lines of the newest manager log file, each line
// redacted by the logging package.
func (a *App) LogsTail(lines int) ([]string, error) {
	tail, err := logging.TailLatest(a.dirs.LogsDir, lines)
	if err != nil {
		a.logger.Error("读取日志失败", "error", err)
		return nil, internalError("读取日志失败，请稍后重试。")
	}
	return tail, nil
}
