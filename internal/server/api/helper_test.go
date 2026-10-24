// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/app"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/artifact"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/config"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/logging"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/pack"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/plugin"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/process"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/runtime"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/job"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/security"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
)

// testPassword is the initial administrator password of every test env.
const testPassword = "initial-password-1"

// testVersion is the manager version reported by /api/health and /api/state.
const testVersion = "test-version"

// fakeCreds is an in-memory security.Credentials implementation.
type fakeCreds struct {
	mu        sync.Mutex
	password  string
	pending   bool
	verifyErr error
}

func (f *fakeCreds) VerifyPassword(plain string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.verifyErr != nil {
		return false, f.verifyErr
	}
	if plain == "" {
		return false, nil
	}
	return plain == f.password, nil
}

func (f *fakeCreds) ChangePassword(oldPassword, newPassword string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if oldPassword != f.password {
		return errors.New("当前密码不正确")
	}
	if newPassword == f.password {
		return errors.New("新密码不能与当前密码相同")
	}
	f.password = newPassword
	f.pending = false
	return nil
}

func (f *fakeCreds) InitialPasswordPending() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pending
}

// fakeApp is an in-memory api.App used by the handler tests. Instance storage is
// the real store (temporary data root); process/runtime behaviour is simulated
// through the hooks and live/running snapshots.
type fakeApp struct {
	mu   sync.Mutex
	st   *store.Store
	dirs config.Dirs

	runtimes     []app.RuntimeInfo
	available    []string
	availableErr error
	logs         []string
	logsErr      error
	settingsErr  error
	deleteErr    error
	deleteRunErr error
	logsTailSeen []int
	live         map[string]app.LiveStatus
	running      []app.InstanceRuntimeStatus
	// maxRunning mirrors app.App.MaxRunningInstances; newTestEnv sets the
	// production default (1) and tests may override it (0 = unlimited).
	maxRunning int

	launchHook  func(ctx context.Context, id string, logf func(string)) (app.LaunchResult, error)
	installHook func(ctx context.Context, version string, logf func(string)) (runtime.InstallResult, error)
	probeHook   func(ctx context.Context, version string, logf func(string)) (app.ProbeResult, error)
}

func (f *fakeApp) CreateInstance(ctx context.Context, in store.CreateInstanceInput) (store.Instance, error) {
	if err := ctx.Err(); err != nil {
		return store.Instance{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if in.Port == 0 {
		port := 3081
		for {
			used := false
			for _, instance := range f.st.ListInstances() {
				if instance.Port == port {
					used = true
					break
				}
			}
			if !used {
				break
			}
			port++
		}
		in.Port = port
	} else {
		for _, instance := range f.st.ListInstances() {
			if instance.Port == in.Port {
				return store.Instance{}, apierr.New(http.StatusConflict, apierr.CodeConflictPortBusy,
					"端口已被占用。")
			}
		}
	}
	instance, err := f.st.CreateInstance(in)
	if err != nil {
		return store.Instance{}, mapTestStoreError(err)
	}
	return instance, nil
}

func (f *fakeApp) UpdateInstance(_ context.Context, id string, name *string, port *int) (store.Instance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	current, ok := f.st.GetInstance(id)
	if !ok {
		return store.Instance{}, apierr.New(http.StatusNotFound, apierr.CodeNotFound, "实例不存在。")
	}
	if port != nil && *port != current.Port {
		if live, tracked := f.live[id]; tracked && isActiveStatus(live.StatusCode) {
			return store.Instance{}, apierr.New(http.StatusConflict, apierr.CodeConflictInstanceRunning,
				"实例正在运行，不能修改端口；请先停止实例。")
		}
		for _, other := range f.st.ListInstances() {
			if other.ID != id && other.Port == *port {
				return store.Instance{}, apierr.New(http.StatusConflict, apierr.CodeConflictPortBusy,
					"端口已被占用。")
			}
		}
	}
	updated, err := f.st.UpdateInstance(id, store.UpdateInstanceInput{Name: name, Port: port})
	if err != nil {
		return store.Instance{}, mapTestStoreError(err)
	}
	return updated, nil
}

func (f *fakeApp) DeleteInstance(_ context.Context, id string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return "", f.deleteErr
	}
	if _, ok := f.st.GetInstance(id); !ok {
		return "", apierr.New(http.StatusNotFound, apierr.CodeNotFound, "实例不存在。")
	}
	if live, tracked := f.live[id]; tracked && isActiveStatus(live.StatusCode) {
		return "", apierr.New(http.StatusConflict, apierr.CodeConflictInstanceRunning,
			"实例正在运行，不能删除；请先停止实例。")
	}
	if err := f.st.DeleteInstance(id); err != nil {
		return "", mapTestStoreError(err)
	}
	return filepath.Join(f.dirs.TrashDir, "instance-"+id), nil
}

func (f *fakeApp) LaunchInstance(ctx context.Context, instanceID string, logf func(string)) (app.LaunchResult, error) {
	if f.launchHook != nil {
		return f.launchHook(ctx, instanceID, logf)
	}
	logf("正在启动实例进程…")
	f.mu.Lock()
	f.live[instanceID] = app.LiveStatus{
		InstanceID:    instanceID,
		StatusCode:    process.StatusRunning,
		StatusMessage: "运行中",
		Ready:         true,
	}
	f.mu.Unlock()
	return app.LaunchResult{InstanceID: instanceID, Status: "运行中", WebURL: "/workbench/" + instanceID + "/"}, nil
}

func (f *fakeApp) StopInstance(_ context.Context, instanceID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.live, instanceID)
	return nil
}

func (f *fakeApp) LiveInstance(instanceID string) (app.LiveStatus, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	live, ok := f.live[instanceID]
	return live, ok
}

func (f *fakeApp) Running() []app.InstanceRuntimeStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]app.InstanceRuntimeStatus(nil), f.running...)
}

func (f *fakeApp) MaxRunningInstances() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxRunning
}

func (f *fakeApp) InstallRuntime(ctx context.Context, version string, logf func(string)) (runtime.InstallResult, error) {
	if f.installHook != nil {
		return f.installHook(ctx, version, logf)
	}
	logf("pnpm install " + version)
	f.mu.Lock()
	replaced := false
	for index := range f.runtimes {
		if f.runtimes[index].Version == version {
			f.runtimes[index].Installed = true
			replaced = true
			break
		}
	}
	if !replaced {
		f.runtimes = append(f.runtimes, app.RuntimeInfo{Version: version, Installed: true, Compatibility: "verified"})
	}
	f.mu.Unlock()
	return runtime.InstallResult{Version: version, EntryPath: "/data/runtimes/" + version}, nil
}

func (f *fakeApp) ProbeRuntime(_ context.Context, version string, logf func(string)) (app.ProbeResult, error) {
	if f.probeHook != nil {
		return f.probeHook(context.Background(), version, logf)
	}
	logf("probe " + version)
	return app.ProbeResult{Version: version, Compatibility: "verified"}, nil
}

func (f *fakeApp) DeleteRuntime(_ context.Context, version string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteRunErr != nil {
		return "", f.deleteRunErr
	}
	index := -1
	for i := range f.runtimes {
		if f.runtimes[i].Version == version {
			index = i
			break
		}
	}
	if index < 0 {
		return "", apierr.New(http.StatusNotFound, apierr.CodeNotFound, "运行时不存在。")
	}
	f.runtimes = append(f.runtimes[:index], f.runtimes[index+1:]...)
	return filepath.Join(f.dirs.TrashDir, "runtime-"+version), nil
}

func (f *fakeApp) Runtimes() []app.RuntimeInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]app.RuntimeInfo(nil), f.runtimes...)
}

func (f *fakeApp) AvailableVersions(_ context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.availableErr != nil {
		return nil, f.availableErr
	}
	return append([]string(nil), f.available...), nil
}

func (f *fakeApp) CachedAvailableVersions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.available...)
}

func (f *fakeApp) UpdateSettings(_ context.Context, settings store.Settings) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.settingsErr != nil {
		return f.settingsErr
	}
	if err := f.st.UpdateSettings(settings); err != nil {
		return mapTestStoreError(err)
	}
	return nil
}

func (f *fakeApp) LogsTail(lines int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logsTailSeen = append(f.logsTailSeen, lines)
	if f.logsErr != nil {
		return nil, f.logsErr
	}
	return append([]string(nil), f.logs...), nil
}

// mapTestStoreError mirrors the app layer's store error mapping.
func mapTestStoreError(err error) error {
	switch {
	case errors.Is(err, store.ErrInvalidInput):
		return apierr.New(http.StatusBadRequest, apierr.CodeValidation, err.Error())
	case errors.Is(err, store.ErrNotFound):
		return apierr.New(http.StatusNotFound, apierr.CodeNotFound, "实例不存在。")
	default:
		return apierr.InternalError()
	}
}

// fakePack is an in-memory api.PackService implementation.
type fakePack struct {
	mu sync.Mutex

	preview      pack.Preview
	previewErr   error
	previewCalls int
	lastFilename string
	importResult pack.ImportResult
	importErr    error
	lastImport   pack.ImportInput
	importCalls  int
	exportResult pack.ExportResult
	exportErr    error
	lastExport   pack.ExportInput
	exportCalls  int
}

func (f *fakePack) Preview(_ context.Context, reader io.Reader, filename string) (pack.Preview, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return pack.Preview{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.previewCalls++
	f.lastFilename = filename
	if f.previewErr != nil {
		return pack.Preview{}, f.previewErr
	}
	if f.preview.ID == "" {
		return pack.Preview{
			ID:            "0123456789ab",
			Name:          "test-pack",
			Format:        pack.FormatDSPack,
			Version:       "1.0.0",
			DSHVersion:    "0.2.1-rc.3",
			Profiles:      []string{"web"},
			UnpackedBytes: int64(len(data)),
			Warnings:      []string{},
		}, nil
	}
	return f.preview, nil
}

func (f *fakePack) Import(_ context.Context, in pack.ImportInput, logf func(string)) (pack.ImportResult, error) {
	if logf != nil {
		logf("import " + in.PreviewID)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.importCalls++
	f.lastImport = in
	if f.importErr != nil {
		return pack.ImportResult{}, f.importErr
	}
	if f.importResult.InstanceID == "" {
		return pack.ImportResult{InstanceID: "i-0123456789ab", Name: in.Name}, nil
	}
	return f.importResult, nil
}

func (f *fakePack) Export(_ context.Context, in pack.ExportInput, logf func(string)) (pack.ExportResult, error) {
	if logf != nil {
		logf("export " + in.InstanceID)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exportCalls++
	f.lastExport = in
	if f.exportErr != nil {
		return pack.ExportResult{}, f.exportErr
	}
	if f.exportResult.ArtifactID == "" {
		return pack.ExportResult{ArtifactID: "0123456789ab", Filename: "export.dspack", Size: 128}, nil
	}
	return f.exportResult, nil
}

// fakePlugins is an in-memory api.PluginService implementation.
type fakePlugins struct {
	mu sync.Mutex

	list           []plugin.DTO
	listErr        error
	listCalls      int
	lastInstanceID string
	lastProfile    string
	mutateResult   plugin.MutateResult
	mutateErr      error
	lastMutate     plugin.MutateInput
	mutateCalls    int
}

func (f *fakePlugins) List(_ context.Context, instanceID, profile string) ([]plugin.DTO, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	f.lastInstanceID = instanceID
	f.lastProfile = profile
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.list == nil {
		return []plugin.DTO{}, nil
	}
	return append([]plugin.DTO(nil), f.list...), nil
}

func (f *fakePlugins) Mutate(_ context.Context, in plugin.MutateInput, logf func(string)) (plugin.MutateResult, error) {
	if logf != nil {
		logf("plugin " + in.Action + " " + in.Spec)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mutateCalls++
	f.lastMutate = in
	if f.mutateErr != nil {
		return plugin.MutateResult{}, f.mutateErr
	}
	if f.mutateResult.Name == "" {
		return plugin.MutateResult{
			InstanceID: in.InstanceID,
			Profile:    in.Profile,
			Action:     in.Action,
			Name:       "test-plugin",
		}, nil
	}
	return f.mutateResult, nil
}

// workbenchRecorder counts /workbench proxy invocations.
type workbenchRecorder struct {
	mu   sync.Mutex
	hits int
}

func (w *workbenchRecorder) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	w.hits++
	w.mu.Unlock()
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = rw.Write([]byte("<html>workbench</html>"))
}

func (w *workbenchRecorder) HitCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.hits
}

// testEnv bundles the API server under test with its real dependencies.
type testEnv struct {
	api       *API
	store     *store.Store
	app       *fakeApp
	security  *security.Manager
	jobs      *job.Manager
	creds     *fakeCreds
	dirs      config.Dirs
	logs      *bytes.Buffer
	workbench *workbenchRecorder
	artifacts *artifact.Store
	pack      *fakePack
	plugins   *fakePlugins
}

// newTestEnv builds a full API stack on a temporary data root. pending controls
// the initial-password state; configure may adjust the Deps before api.New
// (e.g. to enable the M4 static file server).
func newTestEnv(t *testing.T, pending bool, configure ...func(*Deps)) *testEnv {
	t.Helper()
	dataRoot := filepath.Join(t.TempDir(), "data")
	dirs, err := config.InitDataRoot(config.Config{DataRoot: dataRoot})
	if err != nil {
		t.Fatalf("InitDataRoot() error = %v", err)
	}
	st, err := store.Open(dirs.DataRoot)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	audit, err := logging.OpenAudit(dirs.LogsDir)
	if err != nil {
		t.Fatalf("logging.OpenAudit() error = %v", err)
	}
	t.Cleanup(func() { _ = audit.Close() })

	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	creds := &fakeCreds{password: testPassword, pending: pending}
	manager := security.NewManager(security.Config{}, creds, audit, logger)
	jobs := job.NewManager(logger, job.Options{})
	t.Cleanup(jobs.Shutdown)
	workbench := &workbenchRecorder{}
	fake := &fakeApp{st: st, dirs: dirs, live: map[string]app.LiveStatus{}, maxRunning: 1}
	fakePackService := &fakePack{}
	fakePluginService := &fakePlugins{}
	artifactStore, err := artifact.Open(filepath.Join(dirs.CacheDir, "exports"))
	if err != nil {
		t.Fatalf("artifact.Open() error = %v", err)
	}
	deps := Deps{
		Config:    config.Config{Version: testVersion},
		Logger:    logger,
		Dirs:      dirs,
		Store:     st,
		Security:  manager,
		Jobs:      jobs,
		App:       fake,
		Pack:      fakePackService,
		Plugins:   fakePluginService,
		StartedAt: time.Now().Add(-90 * time.Second),
		Workbench: workbench,
		Artifacts: artifactStore,
	}
	for _, apply := range configure {
		apply(&deps)
	}
	server, err := New(deps)
	if err != nil {
		t.Fatalf("api.New() error = %v", err)
	}
	return &testEnv{
		api:       server,
		store:     st,
		app:       fake,
		security:  manager,
		jobs:      jobs,
		creds:     creds,
		dirs:      dirs,
		logs:      logs,
		workbench: workbench,
		artifacts: artifactStore,
		pack:      fakePackService,
		plugins:   fakePluginService,
	}
}

// request performs one HTTP request against the API handler.
func (e *testEnv) request(t *testing.T, method, target string, body []byte, opts ...func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request := httptest.NewRequest(method, target, reader)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for _, opt := range opts {
		opt(request)
	}
	recorder := httptest.NewRecorder()
	e.api.Handler().ServeHTTP(recorder, request)
	return recorder
}

// jsonBody marshals v as a JSON request body.
func jsonBody(t *testing.T, v any) []byte {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return body
}

// session is one authenticated client state (cookies + CSRF token).
type session struct {
	sessionID string
	csrf      string
}

// withSession adds the JSESSIONID + XSRF cookies and the X-XSRF-TOKEN header.
func (s session) withSession() func(*http.Request) {
	return func(r *http.Request) {
		if s.sessionID != "" {
			r.AddCookie(&http.Cookie{Name: security.SessionCookieName, Value: s.sessionID})
		}
		if s.csrf != "" {
			r.AddCookie(&http.Cookie{Name: security.CSRFCookieName, Value: s.csrf})
			r.Header.Set(security.CSRFHeaderName, s.csrf)
		}
	}
}

// cookiesOnly is withSession without the CSRF header (used by CSRF failure tests).
func (s session) cookiesOnly() func(*http.Request) {
	return func(r *http.Request) {
		if s.sessionID != "" {
			r.AddCookie(&http.Cookie{Name: security.SessionCookieName, Value: s.sessionID})
		}
		if s.csrf != "" {
			r.AddCookie(&http.Cookie{Name: security.CSRFCookieName, Value: s.csrf})
		}
	}
}

// login authenticates with the given password and returns the parsed cookies.
func (e *testEnv) login(t *testing.T, password string) session {
	t.Helper()
	rec := e.request(t, http.MethodPost, "/api/auth/login", jsonBody(t, map[string]string{"password": password}))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("login status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
	result := session{}
	for _, cookie := range rec.Result().Cookies() {
		switch cookie.Name {
		case security.SessionCookieName:
			result.sessionID = cookie.Value
		case security.CSRFCookieName:
			result.csrf = cookie.Value
		}
	}
	if result.sessionID == "" || result.csrf == "" {
		t.Fatalf("login did not set both cookies: %v", rec.Result().Cookies())
	}
	return result
}

// loginAs authenticates and fails the test when the login is rejected.
func (e *testEnv) loginAs(t *testing.T) session {
	t.Helper()
	return e.login(t, testPassword)
}

// errorEnvelope is the decoded JSON error body.
type errorEnvelope struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
	TraceID string         `json:"traceId"`
}

// decodeError decodes and asserts the documented error body shape.
func decodeError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) errorEnvelope {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, status, rec.Body.String())
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", contentType)
	}
	var body errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	if body.Code != code {
		t.Fatalf("error code = %q, want %q (message %q)", body.Code, code, body.Message)
	}
	if body.Message == "" {
		t.Fatal("error body has an empty message")
	}
	if body.Details == nil {
		t.Fatal("error body details must be an object, got null")
	}
	if body.TraceID == "" {
		t.Fatal("error body has no traceId")
	}
	if header := rec.Header().Get("X-Trace-Id"); header != body.TraceID {
		t.Fatalf("X-Trace-Id header = %q, body traceId = %q", header, body.TraceID)
	}
	if len(body.TraceID) != 12 {
		t.Fatalf("traceId = %q, want 12 hex characters", body.TraceID)
	}
	if _, err := hex.DecodeString(body.TraceID); err != nil {
		t.Fatalf("traceId = %q is not hex: %v", body.TraceID, err)
	}
	return body
}

// decodeJSONResponse decodes a JSON success body.
func decodeJSONResponse[T any](t *testing.T, rec *httptest.ResponseRecorder, status int) T {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, status, rec.Body.String())
	}
	var value T
	if err := json.Unmarshal(rec.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return value
}

// createTestInstance creates a configured instance through the store.
func (e *testEnv) createTestInstance(t *testing.T, name string, port int) store.Instance {
	t.Helper()
	instance, err := e.store.CreateInstance(store.CreateInstanceInput{
		Name:    name,
		Version: "0.2.1-rc.3",
		Profile: "web",
		Port:    port,
	})
	if err != nil {
		t.Fatalf("store.CreateInstance() error = %v", err)
	}
	return instance
}

// markRunning marks an instance as ready/running in the fake app.
func (e *testEnv) markRunning(instanceID string, bootstrapQuery string) {
	e.app.mu.Lock()
	defer e.app.mu.Unlock()
	live := app.LiveStatus{
		InstanceID:    instanceID,
		StatusCode:    process.StatusRunning,
		StatusMessage: "运行中",
		Ready:         true,
	}
	if bootstrapQuery != "" {
		live.Bootstrap = mustURL("http://127.0.0.1:3081/?" + bootstrapQuery)
	}
	e.app.live[instanceID] = live
}

// mustURL parses a URL or panics.
func mustURL(raw string) *url.URL {
	parsed, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return parsed
}

// waitForJobState polls GET /api/jobs/{id} until the job reaches wantState.
func (e *testEnv) waitForJobState(t *testing.T, sess session, id string, want job.State) job.View {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last job.View
	for time.Now().Before(deadline) {
		rec := e.request(t, http.MethodGet, "/api/jobs/"+id, nil, sess.withSession())
		if rec.Code == http.StatusOK {
			last = decodeJSONResponse[job.View](t, rec, http.StatusOK)
			if last.State == want {
				return last
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s did not reach state %s (last = %+v)", id, want, last)
	return job.View{}
}

// freePort returns a loopback TCP port that is currently free.
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("listener.Close() error = %v", err)
	}
	return port
}

// startBlockingJob starts a job that blocks until it is cancelled and returns
// its id. The returned stop function cancels the job and waits for it to end.
func (e *testEnv) startBlockingJob(t *testing.T) (string, func()) {
	t.Helper()
	view, err := e.jobs.Start(job.KindInstallRuntime, 30*time.Second, func(ctx context.Context, j *job.Job) (any, error) {
		j.Phase("阻塞中", "测试用阻塞任务。")
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatalf("jobs.Start() error = %v", err)
	}
	stop := func() {
		if _, err := e.jobs.Cancel(view.ID); err != nil {
			t.Logf("cancel blocking job: %v", err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if !e.jobs.Busy() {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Logf("blocking job did not stop")
	}
	return view.ID, stop
}
