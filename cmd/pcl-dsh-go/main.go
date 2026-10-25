// SPDX-License-Identifier: GPL-3.0-or-later

// Command pcl-dsh-go is the PCL-DSH-Go manager: a small Go service that manages
// one DeepSeek Harness instance on a Linux host and reverse-proxies its
// workbench under /workbench/{id}/.
//
// M1 wiring: config -> logging/audit -> data root -> single-instance lock ->
// store + admin credentials -> security (sessions/CSRF) -> app orchestrator ->
// job manager -> v1 API server. On SIGINT/SIGTERM the manager stops accepting
// requests, cancels running jobs, stops the DSH instance(s) it owns and then
// releases the lock.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/app"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/artifact"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/config"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/logging"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/pack"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/plugin"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/runtime"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/safe"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/job"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/security"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/tools"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

const (
	// httpShutdownTimeout bounds graceful HTTP shutdown.
	httpShutdownTimeout = 5 * time.Second
	// instanceShutdownTimeout bounds stopping manager-owned DSH instances.
	instanceShutdownTimeout = 30 * time.Second
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// run performs the full startup/shutdown cycle and returns the process exit
// code; deferred cleanup guarantees credentials, lock and log files are
// released even on failure.
func run(args []string) int {
	startedAt := time.Now()

	cfg, err := config.Load(args, os.Getenv)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stdout, config.UsageText())
			return 0
		}
		fmt.Fprintf(os.Stderr, "启动参数错误：%v\n\n%s\n", err, config.UsageText())
		return 2
	}
	if cfg.ShowVersion {
		fmt.Println(version)
		return 0
	}
	cfg.Version = version

	dirs, err := config.InitDataRoot(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化数据目录失败：%v\n", err)
		return 1
	}
	logger, closeLogs, err := logging.Setup(dirs.LogsDir, version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化日志失败：%v\n", err)
		return 1
	}
	defer func() {
		if closeErr := closeLogs(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "关闭日志失败：%v\n", closeErr)
		}
	}()
	logger.Info("pcl-dsh-go 正在启动",
		"address", cfg.ServerAddress,
		"port", cfg.ServerPort,
		"dataRoot", dirs.DataRoot,
		"toolRoot", cfg.ToolRoot,
		"pid", os.Getpid(),
	)

	audit, err := logging.OpenAudit(dirs.LogsDir)
	if err != nil {
		logger.Error("初始化审计日志失败", "error", err)
		return 1
	}
	defer func() {
		if closeErr := audit.Close(); closeErr != nil {
			logger.Warn("关闭审计日志失败", "error", closeErr)
		}
	}()

	lock, err := config.AcquireLock(dirs.DataRoot)
	if err != nil {
		logger.Error("获取数据目录单实例锁失败", "error", err)
		return 1
	}
	defer func() {
		if closeErr := lock.Close(); closeErr != nil {
			logger.Warn("释放数据目录单实例锁失败", "error", closeErr)
		}
	}()

	instanceStore, err := store.Open(dirs.DataRoot)
	if err != nil {
		logger.Error("打开实例配置失败", "error", err)
		return 1
	}
	defer func() {
		if closeErr := instanceStore.Close(); closeErr != nil {
			logger.Warn("关闭实例配置失败", "error", closeErr)
		}
	}()

	admin, err := store.OpenAdmin(dirs.DataRoot)
	if err != nil {
		logger.Error("初始化管理员凭据失败", "error", err)
		return 1
	}
	defer func() {
		if closeErr := admin.Close(); closeErr != nil {
			logger.Warn("关闭管理员凭据失败", "error", closeErr)
		}
	}()
	if admin.InitialPasswordPending() {
		// Never log the password itself: only where the operator can read it.
		logger.Warn("管理员仍使用初始密码，请登录后立即修改", "initialPasswordFile", admin.InitialPasswordPath())
	}

	securityManager := security.NewManager(security.Config{}, admin, audit, logger)

	// A missing toolchain must not prevent the manager from starting: the
	// runtime/instance endpoints report an actionable error instead.
	var toolchain *tools.Tools
	if located, locateErr := tools.Locate(cfg.ToolRoot, dirs.DataRoot); locateErr != nil {
		logger.Warn("未定位到内置 Node/pnpm 工具链，运行时安装与实例启动将不可用", "error", locateErr)
	} else {
		toolchain = located
	}

	application, err := app.New(app.Deps{
		Config:              cfg,
		Dirs:                dirs,
		Logger:              logger,
		Store:               instanceStore,
		Tools:               toolchain,
		MaxRunningInstances: &cfg.MaxRunningInstances,
	})
	if err != nil {
		logger.Error("初始化应用失败", "error", err)
		return 1
	}

	// Pack/artifact/plugin services share the same runtime.Service instance as
	// the app so capability/probe caches cannot diverge.
	runtimeService := application.RuntimeService()
	settings := instanceStore.Settings()
	artifactStore, err := artifact.Open(filepath.Join(dirs.CacheDir, "exports"))
	if err != nil {
		logger.Error("初始化 artifact 存储失败", "error", err)
		return 1
	}
	if cleanupErr := artifactStore.Cleanup(time.Now()); cleanupErr != nil {
		logger.Warn("清理过期 artifact 失败", "error", cleanupErr)
	}
	packService, err := pack.New(pack.Options{
		DataRoot:  dirs.DataRoot,
		Dirs:      dirs,
		Store:     instanceStore,
		Safe:      safe.DefaultLimits(),
		Runtime:   packRuntimeAdapter{service: runtimeService, registry: settings.Registry},
		App:       application,
		Artifacts: artifactStore,
		Logger:    logger,
	})
	if err != nil {
		logger.Error("初始化整合包服务失败", "error", err)
		return 1
	}
	packService.CleanupExpired(time.Now())
	pluginService, err := plugin.New(plugin.Options{
		Dirs:     dirs,
		Store:    instanceStore,
		Tools:    toolchain,
		Runtime:  runtimeService,
		Registry: settings.Registry,
		Proxy:    settings.Proxy,
		Logger:   logger,
	})
	if err != nil {
		logger.Error("初始化插件服务失败", "error", err)
		return 1
	}

	jobs := job.NewManager(logger, job.Options{})

	managerServer, err := server.New(server.Deps{
		Config:    cfg,
		Logger:    logger,
		Dirs:      dirs,
		Store:     instanceStore,
		Security:  securityManager,
		Jobs:      jobs,
		App:       application,
		Pack:      packService,
		Plugins:   pluginService,
		Artifacts: artifactStore,
		StaticDir: cfg.StaticDir,
		StartedAt: startedAt,
	})
	if err != nil {
		logger.Error("初始化 HTTP 服务失败", "error", err)
		return 1
	}

	address := net.JoinHostPort(cfg.ServerAddress, strconv.Itoa(cfg.ServerPort))
	listener, err := net.Listen("tcp", address)
	if err != nil {
		logger.Error("监听端口失败", "address", address, "error", err)
		return 1
	}
	httpServer := &http.Server{
		Addr:              address,
		Handler:           managerServer.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- httpServer.Serve(listener)
	}()
	logger.Info("HTTP 服务已就绪",
		"address", listener.Addr().String(),
		"health", "/api/health",
		"startupMs", time.Since(startedAt).Milliseconds(),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	exitCode := 0
	select {
	case <-ctx.Done():
		logger.Info("收到退出信号，开始优雅停机", "httpTimeout", httpShutdownTimeout.String())
	case serveErrValue := <-serveErr:
		if !errors.Is(serveErrValue, http.ErrServerClosed) {
			logger.Error("HTTP 服务异常退出", "error", serveErrValue)
			exitCode = 1
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
	if shutdownErr := httpServer.Shutdown(shutdownCtx); shutdownErr != nil {
		logger.Warn("HTTP 优雅停机未在限期内完成，强制关闭", "error", shutdownErr)
		_ = httpServer.Close()
	}
	cancel()

	// Cancel in-flight jobs before stopping instances so that a running launch
	// task cannot race the instance shutdown.
	jobs.Shutdown()

	instanceCtx, cancelInstances := context.WithTimeout(context.Background(), instanceShutdownTimeout)
	if shutdownErr := application.Shutdown(instanceCtx); shutdownErr != nil {
		logger.Error("停止实例失败", "error", shutdownErr)
		exitCode = 1
	}
	cancelInstances()

	logger.Info("pcl-dsh-go 已停止")
	return exitCode
}

// packRuntimeAdapter adapts *runtime.Service to pack.RuntimeAccess: the pack
// service installs runtimes without knowing which npm registry is configured.
type packRuntimeAdapter struct {
	service  *runtime.Service
	registry string
}

func (a packRuntimeAdapter) Installed(version string) bool {
	return a.service.Installed(version)
}

func (a packRuntimeAdapter) Install(ctx context.Context, version string, logf func(string)) (runtime.InstallResult, error) {
	registry := strings.TrimSpace(a.registry)
	if registry == "" {
		registry = runtime.DefaultRegistry
	}
	return a.service.Install(ctx, version, registry, logf)
}
