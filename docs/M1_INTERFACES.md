# M1 冻结接口（Lead 维护）

> 状态：**M1 冻结（2026-10-24）**。实现可细化内部细节，但跨包签名/语义不得擅自变更；确需变更先报 Lead 并更新本文件。
> 所有包路径：`github.com/LeuJasYoh/PCL-DSH-Go/internal/...`。契约权威：docs/IMPLEMENTATION_SPEC.md §4/§5、docs/DSH_INTEGRATION.md §6.4、docs/ACCEPTANCE.md G1–G5。
> 代码风格：gofmt / go vet / 每包单测；Go 文件首行 SPDX GPL-3.0-or-later；context 贯穿；无全局可变状态。

## 1. M1 范围与包归属（write scope）

| 包/目录 | 职责 | owner |
|---|---|---|
| internal/apierr | 统一错误模型（已有，Lead 维护） | Lead |
| internal/store | + admin.json（BCrypt/初始密码）、trash、settings 校验 | backend-core |
| internal/logging | + TailLatest（日志 tail，脱敏） | backend-core |
| internal/server/security | 会话/CSRF/登录限流/密码/审计 | backend-server |
| internal/server/job | JobManager + SSE + /api/jobs 路由 | backend-jobs |
| internal/server/api + server.go + middleware | 路由/中间件/认证接入/各 REST handler | backend-server |
| internal/app | 编排扩展（Job 友好的 install/launch/probe、实例/运行时删除、端口分配） | runtime-process |
| internal/runtime | + Remove/Entries（安装列表/兼容标记） | runtime-install |
| internal/directaccess | SSH 隧道命令 + 一次性 token URL DTO | proxy |
| internal/server/proxy | 保持语义；补 Authorizer 登录门槛测试 | proxy |
| cmd/pcl-dsh-go | 接线（admin/security/jobs/api） | Lead |
| scripts/, testdata/, docs/evidence/m1/, docs/M1_RESULTS.md | M1 契约/真实 DSH 验收 | qa-release |

## 2. apierr（已实现）

```go
type Error struct { Status int; Code, Message string; Details any }
func New(status int, code, message string) *Error
func Newf(status int, code, format string, args ...any) *Error
func As(err error) (*Error, bool)
func From(err error) *Error   // 未知错误 → 500 INTERNAL（调用方负责记录原错误）
func (e *Error) WithDetails(details any) *Error
```
错误码常量见 `internal/apierr/apierr.go`（与 IMPLEMENTATION_SPEC §4.4 对齐：UNAUTHORIZED/LOGIN_FAILED/LOGIN_LOCKED/FORBIDDEN/CSRF_INVALID/PASSWORD_CHANGE_REQUIRED/BAD_REQUEST/VALIDATION/NOT_FOUND/CONFLICT_*/INSTANCE_NOT_READY/PAYLOAD_TOO_LARGE/UNSUPPORTED_RUNTIME/INTERNAL/UPSTREAM_*/TIMEOUT/INTERRUPTED）。**API/编排/Job 层的可预期错误一律返回 *apierr.Error。**

## 3. store 扩展（backend-core）

```go
package store

// OpenAdmin 打开/引导 <data-root>/config/admin.json（schemaVersion 1，0600）。
// 首次（文件不存在）：生成 24 位字母数字初始密码，BCrypt cost 10 写入 admin.json
// （initialPasswordPending=true），同时写 <data-root>/initial-password（0600）；
// 已存在且 pending=true：保留 initial-password；pending=false：删除残留 initial-password。
func OpenAdmin(dataRoot string) (*AdminStore, error)
func (a *AdminStore) Close() error
func (a *AdminStore) InitialPasswordPending() bool
func (a *AdminStore) InitialPasswordPath() string
func (a *AdminStore) VerifyPassword(plain string) (bool, error) // bcrypt compare；空密码 false
func (a *AdminStore) ChangePassword(oldPassword, newPassword string) error
// 校验 old（bcrypt）、new 8–64 字符且不等于 old；写新 hash；pending=false；删除 initial-password。
// 危险操作日志由调用方记录，绝不可打印密码。

// MoveToTrash 把已存在的 src 移到 <data-root>/trash/<kind>-<name>-<unixMillis>；
// kind ∈ {instance,runtime}；返回目标路径。目标同名冲突时追加随机后缀。跨设备/失败返回可操作错误。
func MoveToTrash(dataRoot, kind, srcPath string) (string, error)
```
- settings 校验扩展：`DirectAccessSSHUser` 允许 `[A-Za-z0-9._-]{1,32}`，默认 `root`；非法 → UpdateSettings 返回可识别 error（api 层转 400 VALIDATION）。registry/proxy 校验沿用现有实现。
- 现有 CRUD/原子写/0600/端口唯一等语义不变。

## 4. logging 扩展（backend-core）

```go
package logging

// TailLatest 读取 <logsDir> 中最新的 manager-YYYY-MM-DD.log 的最后 lines 行
// （1–2000，0 或越界按默认 800/上限截断），每行 Redact 后返回；文件不存在返回空切片+error。
func TailLatest(logsDir string, lines int) ([]string, error)
```

## 5. security（backend-server）

```go
package security

type Config struct {
    SessionTTL       time.Duration // 默认 12h（滑动）
    LoginMaxFailures int           // 默认 5
    LoginWindow      time.Duration // 默认 60s
    LoginLockFor     time.Duration // 默认 60s
}

// Credentials 由 store.AdminStore 实现（不 import store，避免耦合）。
type Credentials interface {
    VerifyPassword(plain string) (bool, error)
    ChangePassword(oldPassword, newPassword string) error
    InitialPasswordPending() bool
}

type Session struct {
    ID        string
    CSRF      string
    CreatedAt time.Time
    ExpiresAt time.Time
}

type LoginResult struct { SessionID, CSRFToken string; ExpiresAt time.Time }

type Manager struct{ /* ... */ }

func NewManager(cfg Config, creds Credentials, audit *logging.Audit, logger *slog.Logger) *Manager

// Login 校验密码并按 IP 限流；成功返回新会话（废止旧会话由 API 层用旧 Cookie 调 Logout）。
// 错误：*apierr.Error{401 LOGIN_FAILED} 或 {423 LOGIN_LOCKED, Details{"retryAfterSeconds":n}}。
// 审计：login_success/login_failed/login_locked（含 ip，密码绝不记录）。
func (m *Manager) Login(password, clientIP string) (LoginResult, error)

func (m *Manager) Logout(sessionID string) // 幂等
func (m *Manager) Authenticate(r *http.Request) (*Session, bool) // 读 JSESSIONID、过期清理、滑动续期
func (m *Manager) SessionCount() int
func (m *Manager) ChangePassword(sessionID, oldPassword, newPassword string) error // *apierr.Error{400 VALIDATION}
func (m *Manager) InitialPasswordPending() bool
func (m *Manager) LoginLocked(clientIP string) (retryAfterSeconds int, locked bool) // 供登录接口提前返回 423

// CSRF 双提交：Cookie XSRF-TOKEN（可读、SameSite=Lax）与 Header X-XSRF-TOKEN 常量时间相等。
func (m *Manager) ValidateCSRF(r *http.Request) bool
func (m *Manager) CSRFToken(session *Session) string
func SetSessionCookie(w http.ResponseWriter, sessionID string, ttl time.Duration) // JSESSIONID HttpOnly SameSite=Lax Path=/
func ClearSessionCookie(w http.ResponseWriter)
func SetCSRFCookie(w http.ResponseWriter, token string, ttl time.Duration) // XSRF-TOKEN 非 HttpOnly SameSite=Lax
func NewCSRFToken() string // 32 hex，crypto/rand
```
- 登录成功响应还必须下发 XSRF Cookie（API 层用 SetCSRFCookie）。
- 审计事件：login_success/login_failed/login_locked/logout/password_changed；均带 ip（clientIP 由 API 层从 RemoteAddr 计算，可信反代场景 M2 再议）。

## 6. job（backend-jobs）

```go
package job

type Kind string
const (
    KindLaunchInstance Kind = "LAUNCH_INSTANCE"
    KindInstallRuntime Kind = "INSTALL_RUNTIME"
    KindProbeRuntime   Kind = "PROBE_RUNTIME"
)

type State string
const (StateRunning State = "RUNNING"; StateSucceeded = "SUCCEEDED"; StateFailed = "FAILED"; StateCancelled = "CANCELLED")

type ErrorView struct { Code, Message string; Details any }
type View struct {
    ID         string    `json:"id"`         // "j-" + 12hex
    Type       Kind      `json:"type"`
    State      State     `json:"state"`
    Phase      string    `json:"phase"`
    Message    string    `json:"message"`
    CreatedAt  int64     `json:"createdAt"`  // 毫秒
    StartedAt  *int64    `json:"startedAt"`
    FinishedAt *int64    `json:"finishedAt"`
    LastSeq    int64     `json:"lastSeq"`
    Result     any       `json:"result"`
    Error      *ErrorView `json:"error"`
}

type Task func(ctx context.Context, j *Job) (result any, err error)

type Job struct{ /* ... */ }
func (j *Job) Logf(line string)                 // event log
func (j *Job) Phase(phase, message string)      // event state

type Options struct {
    Heartbeat   time.Duration // 默认 15s
    MaxEvents   int           // 默认 4000 环形
    MaxHistory  int           // 默认 20（GET /api/jobs 最近 20）
    MaxDuration time.Duration // 默认 20min（Start 可传 per-task timeout）
    Now         func() time.Time
}

type Event struct { Seq int64; Name string; Data []byte } // Data 为 JSON

func NewManager(logger *slog.Logger, opts Options) *Manager
func (m *Manager) Start(kind Kind, timeout time.Duration, task Task) (*View, error)
// 全局同一时间仅 1 个变更任务：忙时返回 *apierr.Error{409 CONFLICT_BUSY}。
// timeout<=0 用 Options.MaxDuration；到期 → FAILED detail code TIMEOUT。
func (m *Manager) Busy() bool
func (m *Manager) List() []View            // 最近 MaxHistory，按创建时间倒序
func (m *Manager) Get(id string) (View, bool)
func (m *Manager) Cancel(id string) (View, error) // 404 NOT_FOUND；已终态 409 CONFLICT_JOB_FINISHED；否则取消 ctx，最终 CANCELLED
func (m *Manager) Subscribe(id string, afterSeq int64) (history []Event, events <-chan Event, cancel func(), err error)
func (m *Manager) Shutdown()               // 取消所有活动任务（进程退出时调用）
func (m *Manager) Register(mux *http.ServeMux)
// 注册：GET /api/jobs、GET /api/jobs/{id}、GET /api/jobs/{id}/events（SSE）、POST /api/jobs/{id}/cancel
```
SSE 契约（IMPLEMENTATION_SPEC §4.5）：
- 每事件 `id: <seq>\nevent: <name>\ndata: <json>\n\n`；事件名 state/log/result/error。
- data：state `{jobId,seq,ts,state,phase,message}`；log `{jobId,seq,ts,level:"info",line}`；result `{jobId,state,result}`；error `{jobId,state,code,message,details}`。
- 15s 心跳注释 `": ping"\n\n"`（不占 seq）；支持 `Last-Event-ID` 补发（环形缓存）；终态事件发完关闭；连接最长 30min。
- JSON 错误响应 `{code,message,traceId?}`（traceId 取响应头 X-Trace-Id）；404 NOT_FOUND、409 CONFLICT_JOB_FINISHED。
- 任务错误映射：`*apierr.Error` → 其 code/message/details；ctx 取消 → CANCELLED；超时 → FAILED + TIMEOUT；其它 → INTERNAL（原错误只进日志）。

## 7. app 扩展（runtime-process）

保持 M0 已实现语义（进程/ready/token/单实例/端口/探测缓存/proxy 目标），替换掉对 internal/server 的依赖（改用 internal/apierr），并新增：

```go
package app

type LaunchResult struct { InstanceID, Status, WebURL string }
type ProbeResult struct { Version, Compatibility string }
type RuntimeInfo struct { Version string; Installed bool; Compatibility string } // Installed 恒 true（已装列表）
type LiveStatus struct {
    InstanceID    string
    Port          int
    StatusCode    string // process.StatusXxx
    StatusMessage string // 中文
    Ready         bool
    Bootstrap     *url.URL // 仅内存，禁止序列化/日志
}
type StateSnapshot struct { CurrentInstanceID string; Running []InstanceRuntimeStatus }

func (a *App) InstallRuntime(ctx context.Context, version string, logf func(string)) (runtime.InstallResult, error)
func (a *App) ProbeRuntime(ctx context.Context, version string, logf func(string)) (ProbeResult, error)
func (a *App) LaunchInstance(ctx context.Context, instanceID string, logf func(string)) (LaunchResult, error)
func (a *App) StopInstance(ctx context.Context, instanceID string) error
func (a *App) ResolveProxy(ctx context.Context, instanceID string) (proxy.Target, error) // 语义不变
func (a *App) Running() []InstanceRuntimeStatus
func (a *App) LiveInstance(instanceID string) (LiveStatus, bool) // 无 handle 时 false（未启动）

func (a *App) CreateInstance(ctx context.Context, in store.CreateInstanceInput) (store.Instance, error)
// in.Port==0 → 自动分配：3080–65535 中同时满足 store 未占用 + 127.0.0.1 可 bind；失败 409 CONFLICT_PORT_BUSY。
func (a *App) UpdateInstance(ctx context.Context, id string, name *string, port *int) (store.Instance, error)
// 运行中改端口 → 409 CONFLICT_INSTANCE_RUNNING；未找到 404；校验错误 400。
func (a *App) DeleteInstance(ctx context.Context, id string) (trashedPath string, err error)
// 运行中（或正在启动）→ 409 CONFLICT_INSTANCE_RUNNING；实例 dir → trash；store 删除；选中项清空。
func (a *App) DeleteRuntime(ctx context.Context, version string) (trashedPath string, err error)
// 任一实例引用 → 409 CONFLICT_IN_USE；未安装 404；runtime 目录 → trash；清探测缓存。
func (a *App) Runtimes() []RuntimeInfo          // 已安装（入口校验通过）+ Compatibility（verified/unverified）
func (a *App) AvailableVersions(ctx context.Context) ([]string, error) // npm registry（settings.registry）
func (a *App) CachedAvailableVersions() []string // 最近一次成功查询缓存（state 用；无则空切片）
func (a *App) UpdateSettings(ctx context.Context, settings store.Settings) error // 校验+持久化+runtime.SetProxy
func (a *App) LogsTail(lines int) ([]string, error) // logging.TailLatest(dirs.LogsDir, lines)
func (a *App) Shutdown(ctx context.Context) error
```
- 错误必须返回 `*apierr.Error`：400 VALIDATION、404 NOT_FOUND、409 系列、422 UNSUPPORTED_RUNTIME、500 INTERNAL、504 UPSTREAM_TIMEOUT、502 UPSTREAM_ERROR。
- 变更类操作（install/probe/launch）内部仍有 TryLock 防御；忙 → 409 CONFLICT_BUSY。Job 层单任务已保证上层不会并发发起。
- Job 进度：install 用 pnpm 输出逐行 logf；launch/probe 至少 Phase（“探测运行时/启动进程/等待就绪”）与关键日志；logf 必须脱敏（token 不入日志）。
- 兼容旧 M0 server.Orchestrator 接口的代码全部删除（M0 dev API 下线）。

## 8. runtime 扩展（runtime-install）

```go
func (s *Service) Entries() []Entry // Entry{Version, Compatibility, EntryPath}
func (s *Service) Remove(version string) (trashPath string, err error)
// 校验已安装；<data>/runtimes/<ver> → store.MoveToTrash(dataRoot,"runtime",dir)；清探测缓存；未安装 404。
func (s *Service) CompatibilityOf(version string) string // "verified"/"unverified"（现有 Compatibility 可复用）
```
- 现有 Install/Probe/AvailableVersions/BuildLaunchSpec 语义不变；Install 返回 EntryPath 绝对路径。

## 9. directaccess（proxy）

```go
package directaccess

type Input struct {
    Host       string   // 请求 Host（可能含端口；去端口、校验非空/无控制字符）
    SSHUser    string   // settings.DirectAccessSSHUser（默认 root）
    Port       int
    Ready      bool
    StatusNote string   // 未就绪原因（中文）
    Bootstrap  *url.URL // 内存中的启动 URL；Ready 且非 nil 时生成 directUrl
}
type DTO struct {
    Ready         bool    `json:"ready"`
    Port          int     `json:"port"`
    DirectURL     *string `json:"directUrl"`
    TunnelCommand string  `json:"tunnelCommand"`
    SSHHost       string  `json:"sshHost"`
    SSHUser       string  `json:"sshUser"`
    Note          string  `json:"note"`
}
func Build(in Input) (DTO, error) // Host 非法 → *apierr.Error{400}；命令格式 "ssh -L <port>:127.0.0.1:<port> <user>@<host>"
```
- directUrl = `http://127.0.0.1:<port>/<bootstrap path>?<bootstrap query>`（path 默认 "/"）；token 只允许出现在该 DTO 内存返回，禁止日志/持久化。
- API 层：仅登录可调；Cache-Control: no-store；实例不存在 404；未运行/未就绪也 200 且 ready=false。
- proxy 包本身不改语义；补测 Authorizer（登录）拒绝/放行，验证 /workbench 登录门槛由 server 注入的 Authorizer 完成。

## 10. server / API（backend-server）

```go
package server

type Deps struct {
    Config   config.Config
    Logger   *slog.Logger
    Dirs     config.Dirs
    Store    *store.Store
    Security *security.Manager
    Jobs     *job.Manager
    App      *app.App
    StartedAt time.Time
}
func New(deps Deps) (*Server, error)
func (s *Server) Handler() http.Handler
```
路由/中间件/响应契约：**严格按 docs/IMPLEMENTATION_SPEC.md §4.1–§4.5 与 §5**，要点：
- 公开路由：POST /api/auth/login、GET /api/auth/me、GET /api/health；其余 /api/** 需会话。
- 首登 pending：除 /api/auth/**、/api/health 外（含 /workbench 与全部其它 /api）→ 403 PASSWORD_CHANGE_REQUIRED，`details.endpoint="/api/auth/password"`。
- CSRF：除登录外所有非 GET/HEAD /api/** 必须双提交校验（logout 也要）；/workbench 豁免 CSRF（仅会话）。
- 忙时：其它写 /api/**（auth 与 /api/jobs/{id}/cancel 除外）→ 409 CONFLICT_BUSY（由 Jobs.Busy() 判定）。
- 会话 Cookie：JSESSIONID HttpOnly SameSite=Lax Path=/；XSRF-TOKEN 可读 SameSite=Lax；响应头 X-Trace-Id（12 hex）+ 安全头；错误 JSON {code,message,details,traceId}；请求体 JSON ≤1MiB。
- 登录：204 + 会话/XSRF Cookie；匿名 logout 也 204；GET me 返回 {authenticated,csrfToken,version,initialPasswordPending} 并刷新 XSRF Cookie。
- 实例/运行时/Job/日志/设置/直连/health/state 端点与状态码/DTO/错误码：严格按 §4.2/§4.3；launch/install/probe 返回 202 JobView（Jobs.Start 包装 App 方法）；stop 200；delete 200 {trashedPath}；web-url 409 INSTANCE_NOT_READY；direct-access 200 永远 ready 布尔。
- /workbench 挂 proxy.Handler，Authorizer = 会话校验（+pending 拒绝）；未登录 401。
- GET / 占位页保留（M4 换 WebUI 静态）。删除全部 M0 /api/m0/** 调试接口与 dev-mode 行为（config.DevMode 保留 flag 但不再暴露调试 API；若后续要用可由 Lead 决定）。
- state DTO 见 §4.3；instances 状态用 process 中文状态（未启动/启动中/运行中/停止中/进程运行中，服务未就绪/已停止/端口被占用）；profiles 至少 [instance.Profile]；runtimes 用 App.Runtimes()；status 忙碌时 {code:"RUNNING",message:"进行中：<phase>"} 否则 {code:"READY",message:"就绪"}；warnings 数组（如暂无）。
- 审计（可选）：settings 更新写 audit.Event("settings_updated")，不含敏感值。

## 11. cmd/main 接线（Lead）

```text
config.Load → InitDataRoot → logging.Setup/OpenAudit → AcquireLock → store.Open → store.OpenAdmin
→ security.NewManager → app.New → job.NewManager → server.New
→ http.Server(Handler) → SIGTERM：http.Shutdown → jobs.Shutdown → app.Shutdown → admin/store/lock/log 关闭
```
启动日志打印初始密码文件路径（不打印密码本身）；工具链缺失仍可启动（运行时相关端点报错）。

## 12. 测试与验收门禁（M1）

- 每包单测；HTTP 契约 httptest（G1/G2/G3/G5 后端）；SSE 测试需覆盖事件顺序/Last-Event-ID/心跳（可注入短心跳）/取消/忙 409。
- 真实 DSH（WSL，只 /tmp 独立 data-root）：登录→改密→安装 runtime(Job+SSE)→创建实例→launch(Job+SSE)→/workbench 登录门槛→direct-access→stop→logs tail→settings。证据存 docs/evidence/m1/。
- gofmt/vet/test ./... 全绿；GOOS=linux 构建；Linux 测试二进制走 WSL。
- **M1 不实现**：整包/插件/直连前端/WebUI/Nginx/发行包（M2–M5）。
