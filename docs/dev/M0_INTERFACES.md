# M0 冻结接口（Lead 维护）

> 状态：M0 冻结。实现可以细化内部细节，但**跨包签名与语义**不得擅自变更；确需变更先通知 Lead 并更新本文件。
> 所有包路径：`github.com/LeuJasYoh/PCL-DSH-Go/internal/...`。Go 1.27。无全局可变状态；context 贯穿 I/O/进程。
> 代码风格：gofmt、go vet、每包单测；Go 文件首行加 `// SPDX-License-Identifier: GPL-3.0-or-later`。

## 1. 包与职责

| 包 | 职责 | owner |
|---|---|---|
| internal/config | flags/env、Config、data-root 初始化、flock | backend-core |
| internal/logging | slog 文件+stdout、审计、脱敏 | backend-core |
| internal/tools | Node/pnpm 定位、版本探测、子进程命令/env | backend-core |
| internal/store | instances.json / manager.json、原子写、校验 | runtime-process |
| internal/process | spawn/进程组/token/就绪/停止/状态 | runtime-process |
| internal/runtime | SemVer、registry、cohort、install、probe、launch plan | runtime-install |
| internal/server/proxy | /workbench HTTP/SSE/WS | proxy |
| internal/server | M0 最小服务与接线（Lead） | Lead |

## 2. config

```go
package config

type Config struct {
    Version       string // -ldflags "-X main.version=..." 注入后由 main 传入
    ServerAddress string // --server.address, default 127.0.0.1
    ServerPort    int    // --server.port, default 8080
    DataRoot      string // --pcl.data-root, env PCL_DSH_DATA 兜底; default ./data
    ToolRoot      string // --pcl.tool-root, 空表示按 tools 查找顺序
    DevMode       bool   // --pcl.dev-mode, M0 调试 API 开关（默认 false）
}

func Load(args []string, getenv func(string) string) (Config, error)
// args 为不含 argv[0] 的参数；flag 解析错误返回 error；校验 port 1-65535、data-root 非空。

type Dirs struct {
    DataRoot, ConfigDir, InstancesDir, RuntimesDir, CacheDir, LogsDir, TrashDir string
}

func InitDataRoot(c Config) (Dirs, error)
// 创建 <data-root>/{config,instances,runtimes,cache/{pnpm-store,npm,registry-metadata,probes,uploads,exports,pack-staging},logs,trash}，权限 0700；
// <data-root> 已存在时校验为目录；返回绝对路径。

func AcquireLock(dataRoot string) (io.Closer, error)
// <data-root>/.lock 排他 flock（Linux syscall.Flock；Windows build tag 空实现），拿不到返回清晰错误。
```

## 3. logging

```go
package logging

func Setup(logsDir string, version string) (*slog.Logger, func() error, error)
// JSON：同时写 stdout 与 <logsDir>/manager-YYYY-MM-DD.log；返回 logger 与 Close；级别默认 info（env PCL_DSH_LOG_LEVEL 可覆盖 debug/info/warn/error）。
// logger 固定字段 service=pcl-dsh-go, version。

func Redact(s string) string
// 脱敏：token=、password=、api_key=、authorization/cookie 头、URL query 中 token 等 → "***"；保留其余文本。幂等。

type Audit struct{}
func OpenAudit(logsDir string) (*Audit, error) // <logsDir>/audit-YYYY-MM-DD.log
func (a *Audit) Event(event string, attrs ...any) // 单行 JSON：ts,event,attrs...（调用方保证已脱敏）
func (a *Audit) Close() error
```

## 4. tools

```go
package tools

type Tools struct {
    NodePath    string // 绝对路径
    PnpmScript  string // pnpm.cjs 绝对路径
    NodeVersion string
    PnpmVersion string
    ToolRoot    string // 命中的工具根（可为空=PATH 兜底）
}

func Locate(toolRoot, dataRoot string) (*Tools, error)
// 查找顺序：toolRoot → <dataRoot>/tools → PATH（仅兜底）。Node: tools/node/bin/node | tools/node/node；
// pnpm: tools/pnpm/node_modules/pnpm/bin/pnpm.cjs | tools/pnpm/bin/pnpm.cjs | tools/pnpm/pnpm.cjs。
// 必须同时找到 node 与 pnpm，否则 error（说明尝试路径）。

func (t *Tools) Verify(ctx context.Context) error
// 依次执行 node -v 与 node <PnpmScript> -v（15s 超时），解析版本写回 t.NodeVersion/t.PnpmVersion；失败 error。

func (t *Tools) NodeCommand(ctx context.Context, args ...string) *exec.Cmd
// exec.CommandContext(ctx, t.NodePath, args...)

func (t *Tools) PnpmCommand(ctx context.Context, args ...string) *exec.Cmd
// exec.CommandContext(ctx, t.NodePath, append([]string{t.PnpmScript}, args...)...)

func (t *Tools) Env(base []string) []string
// 返回 PATH=<node bin>:<pnpm home>:...、PNPM_HOME=<pnpm 目录> 等；不继承无关变量（调用方传入基准 env）。
```

## 5. store

```go
package store

type Instance struct {
    ID        string // i- + 12 hex
    Name      string
    Version   string // 精确 SemVer
    Profile   string // [A-Za-z0-9._-]+
    Port      int    // 1-65535
    CreatedAt int64  // 毫秒
    UpdatedAt int64
}

type Settings struct {
    Proxy               string // 默认 ""
    Registry            string // 默认 https://registry.npmjs.org
    DirectAccessSSHUser string // 默认 root
}

type CreateInstanceInput struct { Name, Version, Profile string; Port int }
type UpdateInstanceInput struct { Name *string; Port *int }

type Store struct{ /* ... */ }

func Open(dataRoot string) (*Store, error) // 读取/初始化 <dataRoot>/config/{manager,instances}.json；损坏 schemaVersion 返回 error
func (s *Store) Close() error

func (s *Store) ListInstances() []Instance           // 拷贝，稳定顺序（创建时间）
func (s *Store) GetInstance(id string) (Instance, bool)
func (s *Store) CreateInstance(in CreateInstanceInput) (Instance, error)
func (s *Store) UpdateInstance(id string, in UpdateInstanceInput) (Instance, error) // 只允许 name/port
func (s *Store) DeleteInstance(id string) error      // 仅持久层删除；文件系统目录由调用方处理
func (s *Store) SelectedInstanceID() string
func (s *Store) SelectInstance(id string) error      // 不存在返回 error
func (s *Store) Settings() Settings
func (s *Store) UpdateSettings(Settings) error
```
- 原子写：同目录临时文件→fsync→rename，0644 以内（config 0600）；并发安全 mutex；schemaVersion 1。
- 校验：name 1-40 字符、去首尾空白、禁控制字符与 / \；version 精确 SemVer（本地实现，可复用 runtime 的解析器但不得形成环——store 自带最小校验正则/解析）；profile 正则；端口范围；创建时 id 唯一。

## 6. process

```go
package process

type LaunchSpec struct {
    InstanceID    string
    Command       []string      // argv，Command[0]=node 绝对路径
    Env           []string      // 完整环境（不继承调用方）
    Dir           string        // 工作目录（instances/<id>/workspace）
    Port          int
    LogPath       string        // 合并 stdout/stderr 落盘（已脱敏）
    ReadyTimeout  time.Duration // 0 = 90s
    ReadyInterval time.Duration // 0 = 400ms
}

type Status struct {
    Code      string // STOPPED|STARTING|RUNNING|STOPPING|NOT_READY|PORT_BUSY|EXITED
    Message   string // 中文状态（前端 M1 依赖）：未启动/启动中/运行中/停止中/进程运行中，服务未就绪/端口被占用/已停止
    PID       int
    StartedAt time.Time
}

type Handle struct{ /* ... */ }
type Manager struct{ /* ... */ }

func NewManager(logger *slog.Logger) *Manager
func (m *Manager) Launch(ctx context.Context, spec LaunchSpec) (*Handle, error) // 阻塞至就绪或失败（进程可能仍在：返回 error 时 Status=NOT_READY，调用方可 Stop）
func (m *Manager) Get(instanceID string) (*Handle, bool)
func (m *Manager) Running() []*Handle
func (m *Manager) StopAll(ctx context.Context) error

func (h *Handle) InstanceID() string
func (h *Handle) Status() Status
func (h *Handle) Ready() bool
func (h *Handle) BootstrapURL() *url.URL // 内存中；未就绪 nil。禁止写日志/序列化
func (h *Handle) Stop(ctx context.Context) error
func (h *Handle) Wait() <-chan struct{} // 进程退出时关闭
```
- 语义严格按 docs/DSH_INTEGRATION.md §5：token 只内存；就绪判定 2xx/3xx 或 401/403+token；404 继续；提前退出失败；Stop 先子孙后根 SIGTERM→3s→SIGKILL→4s；端口释放轮询 2s；只杀自有进程树；日志脱敏。
- Manager 内部状态（运行中实例集合）受 mutex 保护；单实例限制由上层（server）做，process 只保证一个 instanceID 一个 handle。

## 7. runtime

```go
package runtime

type Capabilities struct {
    LauncherOptions  []string // 例如 --profile
    AppOptions       []string // 例如 --port --host --no-open
    PluginManagement bool
    WebAlias         bool
    Profile          string
    EntryPath        string
    Version          string
}

type InstallResult struct {
    Version   string
    EntryPath string // node_modules/@deepseek-ai/dsh/<bin>
    Reused    bool   // 已安装
}

type Service struct{ /* ... */ }

func New(logger *slog.Logger, tl *tools.Tools, dataRoot string) *Service

func (s *Service) AvailableVersions(ctx context.Context, registry string) ([]string, error)
func (s *Service) Installed(version string) bool
func (s *Service) Install(ctx context.Context, version, registry string, logf func(string)) (InstallResult, error)
func (s *Service) Probe(ctx context.Context, version, profile string) (Capabilities, error)
func (s *Service) BuildLaunchSpec(inst store.Instance, dirs config.Dirs, tl *tools.Tools, registry, proxy string, caps Capabilities) (process.LaunchSpec, error)
```
- 语义按 docs/DSH_INTEGRATION.md §2/§3/§4：registry 超时/上限/排序；安装命令/workspace/overrides 逐字；staging+原子移动；probe fail-closed；启动命令不得注入账户参数。
- `Install` 的 logf 用于把 pnpm 输出实时转给上层（M0 直接写 logger；M1 接 Job）。
- BuildLaunchSpec 负责目录创建（dsh-home/agents-home/workspace）与 env 白名单（§1.2）。
- entry/bin 校验：必须位于包目录内、存在、realpath 不越界。

## 8. proxy

```go
package proxy

type Target struct {
    InstanceID     string
    Port           int    // 回环上游端口
    BootstrapPath  string // 通常 "/"
    BootstrapQuery string // 例如 "token=..."；仅内存，禁止日志
    Ready          bool
}

var (
    ErrInstanceNotFound = errors.New("instance not found")
    ErrInstanceNotReady = errors.New("instance not ready")
    ErrInstanceNotRunning = errors.New("instance not running")
)

type Resolver interface {
    Resolve(ctx context.Context, instanceID string) (Target, error)
}

type Authorizer func(r *http.Request) error // nil 返回 = 放行；返回错误则 401/403（由 proxy 映射）

func New(logger *slog.Logger, resolver Resolver, authorize Authorizer) *Handler
// Handler.ServeHTTP 处理 /workbench/{id} 与 /workbench/{id}/**（完整路径）。
```
- 语义按 docs/DSH_INTEGRATION.md §6；错误 JSON：{code,message}（404 INSTANCE_NOT_FOUND / 409 INSTANCE_NOT_READY / 502 UPSTREAM_ERROR / 504 UPSTREAM_TIMEOUT / 400 BAD_REQUEST）。
- 不 import 其他 internal 包（只有 stdlib + gorilla/websocket）。
- 单元测试用 httptest mock 上游。

## 9. internal/server + internal/app（Lead，M0 接线）

```go
package server

// Orchestrator 由 internal/app 实现（runtime.Service + process.Manager 组合）。
type Orchestrator interface {
    InstallRuntime(ctx context.Context, version string) (RuntimeInstallResult, error)
    LaunchInstance(ctx context.Context, instanceID string) (InstanceLaunchResult, error)
    StopInstance(ctx context.Context, instanceID string) error
    ResolveProxy(ctx context.Context, instanceID string) (proxy.Target, error)
    Running() []InstanceRuntimeStatus
}

type Deps struct {
    Config       config.Config
    Logger       *slog.Logger
    Store        *store.Store
    Orchestrator Orchestrator
}

func New(deps Deps) (*Server, error)
func (s *Server) Handler() http.Handler
```
- M0 路由：GET /api/health 匿名；GET / 占位页；--pcl.dev-mode 时 /api/m0/** 调试接口（state/list/create/launch/stop/runtimes/install）；/workbench/** 交给 proxy（dev-mode 放行，非 dev-mode 401 fail-closed）；其余 404。
- 启动/安装接口同步阻塞（M1 改 202+Job）；返回 JSON 不含 token，只含 {instanceId,status,webUrl:"/workbench/{id}/"}。
- 错误：`*server.APIError{Status,Code,Message}` → JSON；其它错误 → 500 INTERNAL 且不回显内部文本。

```go
package app

// App 实现 server.Orchestrator，并持有 runtime.Service / process.Manager。
type Deps struct {
    Config config.Config
    Dirs   config.Dirs
    Logger *slog.Logger
    Store  *store.Store
    Tools  *tools.Tools // 可为 nil（工具链缺失时启动仍可用，安装/启动明确报错）
}

func New(deps Deps) (*App, error)
func (a *App) InstallRuntime(...) (server.RuntimeInstallResult, error)
func (a *App) LaunchInstance(...) (server.InstanceLaunchResult, error)
func (a *App) StopInstance(...) error
func (a *App) ResolveProxy(...) (proxy.Target, error)
func (a *App) Running() []server.InstanceRuntimeStatus
func (a *App) Shutdown(ctx context.Context) error // 停止自有实例
```
- 单实例/单任务：App 用 mutex 串行化 install/launch（忙返回 409 CONFLICT_BUSY）；启动前检查最多 1 个运行实例（409 CONFLICT_RUNNING_INSTANCE）与端口占用（409 CONFLICT_PORT_BUSY）；未安装 runtime 时明确报错。
- Launch 流程：Probe 能力（按 version 进程内缓存）→ runtime.BuildLaunchSpec → process.Launch（阻塞至就绪/失败）。
- ResolveProxy：实例存在→进程 handle 存在且 Ready→从 handle 内存 BootstrapURL 取 Path/Query 构造 proxy.Target；否则返回 proxy 的 sentinel error。