# M3 冻结决策（整合包 + 已装插件）

> 状态：**M3 冻结（2026-10-24，Lead）**。跨包签名/语义变更先报 Lead 并更新本文件。
> 权威：docs/DSH_INTEGRATION.md §7/§8、docs/IMPLEMENTATION_SPEC.md §4.2/§4.3/§4.4/§5、docs/ACCEPTANCE.md G6/G7、参考实现 `PCL-DSH-Linux/pcl-dsh-core/.../pack/{SafeArchive,TarSafety,PackService,PackInspection}.java` 与 `runtime/PluginService.java`（只读）。
> M3 范围：安全归档、dspack v2/v3 + Overture snapshot v1 预览/导入/导出、artifact 下载、已装插件列表/安装/更新/移除（Node 桥）；真实样本与 E2E。**不含** WebUI（M4）、发行包（M5）。

## 1. 包归属（write scope）

| 包/目录 | 职责 | owner |
|---|---|---|
| internal/safe | ZIP/TAR 安全归档核心（限值/路径/链接/CRC/压缩比/取消） | backend-core |
| internal/pack | 格式识别/预览/导入/导出（消费 internal/safe、app、runtime、artifact） | runtime-process |
| internal/artifact | artifact 存储（TTL 24h、artifacts.json 原子写）+ GET /api/artifacts/{id} | backend-jobs |
| internal/plugin | 已装插件列表 + Node 桥变更 | runtime-install |
| internal/server/job | 新增 Kind：IMPORT_PACK/EXPORT_PACK/PLUGIN_MUTATE | backend-jobs |
| internal/server/api | packs/plugins 路由与 Job 包装、Deps 扩展 | backend-server |
| cmd/pcl-dsh-go、docs | 服务构造/接线/验收、M3_INTERFACES/README | Lead |
| scripts/、testdata/、docs/evidence/m3/、docs/M3_RESULTS.md | 恶意样本/真实 E2E/报告 | qa-release |

## 2. internal/safe（backend-core）

```go
package safe

type Limits struct {
    MaxArchiveBytes     int64 // 2 GiB
    MaxUnpackedBytes    int64 // 6 GiB
    MaxFileBytes        int64 // 2 GiB
    MaxMetadataBytes    int64 // 4 MiB
    MaxEntries          int   // 400000
    MaxPathLength       int   // 1000
    MaxCompressionRatio int64 // 1000（仅当解压后 >1 MiB 时判定）
}
func DefaultLimits() Limits

type EntryInfo struct {
    Name string // 原始归档路径（未清洗）
    Size int64  // 压缩后大小
    UncompressedSize int64
    Mode fs.FileMode
    IsDir bool
}

type ErrKind string // "unsafe_path"|"entry_conflict"|"limit_exceeded"|"unsupported"|"corrupt"|"cancelled"
type Error struct { Kind ErrKind; Message string }
func (e *Error) Error() string

// OpenZip 打开 ZIP：校验容器（加密/不支持压缩/条目数/元数据大小），
// 校验全部条目路径与冲突（见 §2.1），不落盘。archive ≤ MaxArchiveBytes。
func OpenZip(path string, limits Limits) (*ZipArchive, error)
func (a *ZipArchive) Entries() []EntryInfo
// Extract 解包到 dest（必须不存在或为空目录）：逐条安全校验+父链 symlink 校验+
// 目标仍位于 dest 内+CRC/实际字节一致性+压缩比；支持 ctx 取消；失败返回 error
// 并由调用方清理 dest。拒绝符号链接/硬链接/特殊文件。
func (a *ZipArchive) Extract(ctx context.Context, dest string) error
func (a *ZipArchive) Close() error

// OpenTarGz/Extract 同上（tar.gz；禁止链接/特殊/稀疏；PAX/GNU 元数据大小限制；gzip 前后限）。
func OpenTarGz(path string, limits Limits) (*TarArchive, error)
func (a *TarArchive) Extract(ctx context.Context, dest string) error

// ValidateEntries 单独校验条目列表（供已解压 staging 复验/测试）。
func ValidateEntries(entries []EntryInfo, limits Limits) error
```
§2.1 安全规则（逐项照 DSH_INTEGRATION §7.2，必须全部实现并单测）：
- 归档 ≤2 GiB；解包总量 ≤6 GiB；单文件 ≤2 GiB；metadata ≤4 MiB；条目 ≤400000；路径 ≤1000。
- ZIP 原始路径含反斜杠 → 拒绝；必须相对、非空；禁止 `..`/`.`/空段/尾随点/尾随空格/控制字符/`<>:"|?*`/Windows 保留名（CON/PRN/AUX/NUL/COM1-9/LPT1-9）。
- 路径重复/大小写冲突/Unicode NFC 归一冲突/文件与目录冲突 → 拒绝。
- 禁止符号链接/硬链接/特殊文件；ZIP 加密/不支持压缩 → 拒绝；CRC 与实际解压字节必须一致；压缩率 >1000 且解压 >1 MiB → 拒绝。
- 解包目标父链不得含 symlink/reparse；目标已存在且非普通文件 → 拒绝；失败/取消清理。
- npm tarball（如需要）：必须位于 package/ 下、包名/版本校验、无链接/特殊/稀疏、gzip 前后限、尾随数据检查（可后续导入场景使用）。

## 3. internal/pack（runtime-process）

```go
package pack

type Format string // "dspack" | "overture"
type Preview struct {
    ID string; Name string; Format Format; Version string; DSHVersion string
    Profiles []string; UnpackedBytes int64; Warnings []string
    CreatedAt, ExpiresAt time.Time // TTL 30min
}
type ImportInput struct { PreviewID, Name, Version string }
type ImportResult struct { InstanceID, Name string }
type ExportInput struct { InstanceID, Mode string } // "profile"|"home"
type ExportResult struct { ArtifactID, Filename string; Size int64 }

type Options struct {
    DataRoot string
    Dirs     config.Dirs
    Store    *store.Store
    Safe     SafeLimits // 或直接 safe.DefaultLimits()
    Runtime  RuntimeAccess
    App      AppAccess
    Artifacts ArtifactStore
    Logger   *slog.Logger
}
type RuntimeAccess interface {
    Installed(version string) bool
    Install(ctx context.Context, version string, logf func(string)) (runtime.InstallResult, error)
}
type AppAccess interface {
    CreateInstance(ctx context.Context, in store.CreateInstanceInput) (store.Instance, error)
    DeleteInstance(ctx context.Context, instanceID string) (string, error)
    RestoreProfileDependencies(ctx context.Context, instanceID, profile string, logf func(string)) error
    EntryPath(version string) (string, error)
}
type ArtifactStore interface {
    Add(filename, path, contentType string, size int64) (artifact.Artifact, error)
}

func New(opts Options) (*Service, error)
func (s *Service) Preview(ctx context.Context, reader io.Reader, filename string) (Preview, error)
// 流式落盘 cache/uploads（0600）+SHA256；格式识别（dspack.json v2/v3+manifest v4/v5；dsh-snapshot.json v1；dsh-pack.yaml 明确拒绝）；
// 安全校验（safe.ValidateEntries 或限量解压 manifest/profile.yaml）；preview TTL 30min；不安装/不执行任何包代码。
func (s *Service) Import(ctx context.Context, in ImportInput, logf func(string)) (ImportResult, error)
// 1) 校验 preview（存在/未过期/重算 SHA-256）2) 解析 runtime 版本（请求>manifest.dshVersion>已装兼容>已装最新；缺失则 Install）
// 3) 安全解包到 staging 4) 创建实例（版本/profile/端口自动）5) 填 dsh-home/workspace/agents-home，
// 恢复 profile 依赖（App.RestoreProfileDependencies；pnpm --ignore-scripts --config.ignore-pnpmfile=true；lock 优先 frozen，ERR_PNPM_OUTDATED_LOCKFILE 备份后重试）
// 6) 成功登记实例；失败整体回滚：实例 dir→trash、store 记录删除、staging 清理；Job result {instanceId,name}。
func (s *Service) Export(ctx context.Context, in ExportInput, logf func(string)) (ExportResult, error)
// profile/home；安全排除凭据/会话/日志/缓存/trash 等黑名单；生成 dspack v3+manifest v5（zip 安全路径/大小）；
// 写入 cache/exports，Artifacts.Add（TTL 24h）；返回 {artifactId,filename,size}。
func (s *Service) CleanupExpired(now time.Time)
```
- 错误统一 `*apierr.Error`（400 VALIDATION/404 NOT_FOUND/409 CONFLICT_*/422 UNSUPPORTED_RUNTIME/500 INTERNAL），取消/超时透传 ctx。
- preview/import/export 必须支持 ctx 取消并清理临时文件；不得执行归档中的任何代码/脚本。

## 4. internal/artifact（backend-jobs）

```go
package artifact

type Artifact struct {
    ID, Filename, Path, ContentType string
    Size int64
    CreatedAt, ExpiresAt int64 // ms；TTL 24h
}
type Store struct{}
func Open(dir string) (*Store, error) // dir=cache/exports；artifacts.json schemaVersion 1 原子写 0600
func (s *Store) Add(filename, path, contentType string, size int64) (Artifact, error) // id 12hex；过期旧条目清理
func (s *Store) Get(id string, now time.Time) (Artifact, bool) // 过期→false
func (s *Store) Cleanup(now time.Time) error
func (s *Store) Register(mux *http.ServeMux)
// GET /api/artifacts/{id}（由 api mux 挂载、继承会话/安全头）：流式下载、Content-Disposition attachment、Cache-Control no-store；过期/不存在 404 四字段。
```

## 5. internal/plugin（runtime-install）

```go
package plugin

type DTO struct { Name, Version, Description, Source string } // JSON: name/version/description/source
type MutateInput struct { InstanceID, Profile, Action, Spec string } // action add|update|remove
type MutateResult struct { InstanceID, Profile, Action, Name string }
type Options struct {
    Dirs config.Dirs; Store *store.Store; Tools *tools.Tools; Runtime *runtime.Service
    Registry, Proxy string; Logger *slog.Logger
}
func New(opts Options) (*Service, error)
func (s *Service) List(ctx context.Context, instanceID, profile string) ([]DTO, error)
// profile 不存在/未初始化→空列表（非 404）；实例不存在→404；profile 非法→400。
// deps/optionalDependencies 名称+版本；dsh.profile.bundles（string 或 {name|package}）→source="内置"；
// 已安装 package.json（profile/node_modules 优先，再 runtime node_modules）读 version/description/dsh.bundle；缺失→version="待恢复依赖"。
func (s *Service) Mutate(ctx context.Context, in MutateInput, logf func(string)) (MutateResult, error)
// 前置：实例存在且已停止（运行→409 CONFLICT_INSTANCE_RUNNING）、Jobs 层保证单任务、profile 合法、
// 运行时 pluginManagement=true（Probe 能力，否则 422 UNSUPPORTED_RUNTIME）。
// 命令：node <embedded pluginBridge.mjs> <pnpm.cjs> <dsh-entry> plugin --profile <p> add|update|remove [spec]
//   --config.ignore-scripts=true --config.ignore-pnpmfile=true；workdir=实例 workspace；env=DSH 白名单+registry/proxy；20min 超时；logf 实时。
// remove 必须完整 npm 包名且为 profile 独立依赖（内置 bundle→400）；add 支持 npm 名/坐标/GitHub/file:./相对（锚定 workspace 绝对路径）；
// 拒绝以 - 开头/含控制字符/超长（≤214）spec。Node 桥把 DSH 对 pnpm 的 spawn 路由到内置 pnpm（node pnpm.cjs），失败 fail-closed。
```
- runtime 包（owner 同）：新增 `func (s *Service) EntryPath(version string) (string,error)` 与 `func (s *Service) ProfileInstallEnvironment(dirs config.Dirs, instanceID, profile, registry, proxy string) (dir string, env []string, err error)`（供 app.RestoreProfileDependencies 使用；不改现有签名）。

## 6. app 扩展（runtime-process）

```go
func (a *App) RuntimeInstalled(version string) bool
func (a *App) EntryPath(version string) (string, error) // 透传 runtime
func (a *App) RestoreProfileDependencies(ctx context.Context, instanceID, profile string, logf func(string)) error
// 用内置 node/pnpm 在 instances/<id>/dsh-home/profiles/<profile> 下执行：
//   node pnpm.cjs install --ignore-scripts --config.ignore-pnpmfile=true --registry=<settings.registry> --store-dir=<cache>/pnpm-store [--frozen-lockfile]
// 有 pnpm-lock.yaml 先 frozen；ERR_PNPM_OUTDATED_LOCKFILE 时备份锁文件为 .hdsl-original-pnpm-lock.yaml 后去掉 frozen 重跑；其他错误失败。
```

## 7. job 扩展（backend-jobs）

新增 Kind：`KindImportPack="IMPORT_PACK"`、`KindExportPack="EXPORT_PACK"`、`KindPluginMutate="PLUGIN_MUTATE"`；其余语义不变。
Job result：IMPORT_PACK `{instanceId,name}`；EXPORT_PACK `{artifactId,filename,size}`；PLUGIN_MUTATE `{instanceId,profile,action,name}`。

## 8. API v1 新增路由（backend-server）

| 方法/路径 | 鉴权/CSRF | 请求 | 成功 | 失败 |
|---|---|---|---|---|
| POST /api/packs/preview | 登录/必需 | multipart `file`（流式，≤2 GiB） | 200 PackPreviewDto | 400 VALIDATION；413 PAYLOAD_TOO_LARGE；409 CONFLICT_BUSY（Job 忙） |
| POST /api/packs/import | 登录/必需 | `{previewId,name?,version?}` | 202 JobView IMPORT_PACK | 400；404 preview；409 BUSY |
| POST /api/packs/export | 登录/必需 | `{instanceId,mode:"profile"\|"home"}` | 202 JobView EXPORT_PACK | 400；404；409 BUSY |
| GET /api/artifacts/{id} | 登录/— | — | 文件流（attachment/no-store） | 404（过期） |
| GET /api/instances/{id}/plugins?profile= | 登录/— | — | 200 PluginDto[] | 400 profile 非法；404 实例 |
| POST /api/instances/{id}/plugins | 登录/必需 | `{spec}` | 202 JobView PLUGIN_MUTATE | 400；404；409（busy/instance running）；422 |
| PATCH /api/instances/{id}/plugins/{name...} | 登录/必需 | — | 202 JobView | 同上 |
| DELETE /api/instances/{id}/plugins/{name...} | 登录/必需 | — | 202 JobView | 同上 |
- Deps 扩展：`Pack PackService`、`Plugins PluginService`（接口定义在 api 包，*pack.Service/*plugin.Service 实现）；artifact Store.Register 在 buildHandler 内挂载（继承 middleware 会话/安全头）。
- preview 上传不占全局 Job（发现类），但忙时按 spec 返回 409 CONFLICT_BUSY（与 M1 忙规则一致）；导入/导出/插件变更走 Jobs.Start 单任务。

## 9. 验收（M3 退出）

- internal/safe 恶意样本测试全部拒绝且无文件落盘（穿越/链接/大小写冲突/压缩炸弹/加密/绝对路径/反斜杠/Windows 保留名/超限/CRC 等，≥参考 PackServiceTest 的 25 类）；Go 单测 + 至少一次真实样本。
- preview 合法 dspack v2/v3、Overture v1 返回正确字段；过期 404。
- 导入：Job 成功创建可启动实例；失败整体回滚到 trash，不污染现有实例；导出 profile/home 产物可下载、排除黑名单、可再次 preview/import（往返）。
- 插件：列表与 profile package.json 一致；真实 DSH CLI 安装/更新/移除成功（本地测试插件包），失败给 pnpm/CLI 摘要；运行中/忙 409；内置 bundle 移除 400。
- 真实 DSH E2E（WSL，独立 data-root）：export→preview→import→launch→/workbench；插件 add→列表变化→stop 后生效；证据 docs/evidence/m3/。
- gofmt/vet/test ./... 全绿；GOOS=linux 构建；token/密码/归档内容不泄漏到日志；Windows DSH/参考仓库未改。
