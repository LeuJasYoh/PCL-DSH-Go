// SPDX-License-Identifier: MIT

package security

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/logging"
)

// fakeClock is a manually advanced clock.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeCreds is an in-memory Credentials implementation.
type fakeCreds struct {
	mu        sync.Mutex
	password  string
	pending   bool
	verifyErr error
	changeErr error
}

func (f *fakeCreds) VerifyPassword(plain string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.verifyErr != nil {
		return false, f.verifyErr
	}
	if plain == "" || f.password == "" {
		return false, nil
	}
	return plain == f.password, nil
}

func (f *fakeCreds) ChangePassword(oldPassword, newPassword string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.changeErr != nil {
		return f.changeErr
	}
	if oldPassword != f.password {
		return errors.New("旧密码不正确")
	}
	if newPassword == f.password {
		return errors.New("新密码不能与旧密码相同")
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

func (f *fakeCreds) currentPassword() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.password
}

func (f *fakeCreds) setPending(pending bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pending = pending
}

const testPassword = "initial-password-1"

// newTestManager builds a Manager wired to a fake clock and a real audit log
// in a temporary directory. It returns the audit directory for assertions.
func newTestManager(t *testing.T, pending bool) (*Manager, *fakeCreds, *fakeClock, string) {
	t.Helper()
	logsDir := filepath.Join(t.TempDir(), "logs")
	audit, err := logging.OpenAudit(logsDir)
	if err != nil {
		t.Fatalf("OpenAudit() error = %v", err)
	}
	t.Cleanup(func() { _ = audit.Close() })
	creds := &fakeCreds{password: testPassword, pending: pending}
	clock := newFakeClock()
	manager := NewManager(Config{}, creds, audit, slog.New(slog.DiscardHandler))
	manager.now = clock.Now
	return manager, creds, clock, logsDir
}

// readAudit concatenates every audit file in dir.
func readAudit(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q) error = %v", dir, err)
	}
	var builder strings.Builder
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("ReadFile(%q) error = %v", entry.Name(), err)
		}
		builder.Write(data)
	}
	return builder.String()
}

// requestWithCookies builds a request carrying the session and CSRF cookies;
// when csrf is non-empty the X-XSRF-TOKEN header is set too.
func requestWithCookies(sessionID, csrf string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	if sessionID != "" {
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionID})
	}
	if csrf != "" {
		r.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: csrf})
		r.Header.Set(CSRFHeaderName, csrf)
	}
	return r
}

// requireAPIError asserts err is an *apierr.Error with the given status/code.
func requireAPIError(t *testing.T, err error, status int, code string) *apierr.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %s(%d), got nil", code, status)
	}
	apiErr, ok := apierr.As(err)
	if !ok {
		t.Fatalf("expected *apierr.Error, got %T: %v", err, err)
	}
	if apiErr.Status != status || apiErr.Code != code {
		t.Fatalf("error = %s(%d), want %s(%d)", apiErr.Code, apiErr.Status, code, status)
	}
	return apiErr
}

func requireHexLen(t *testing.T, name, value string, wantBytes int) {
	t.Helper()
	if len(value) != wantBytes*2 {
		t.Fatalf("%s length = %d, want %d hex chars (%d bytes)", name, len(value), wantBytes*2, wantBytes)
	}
	if _, err := hex.DecodeString(value); err != nil {
		t.Fatalf("%s = %q is not hex: %v", name, value, err)
	}
}

func TestLoginSuccess(t *testing.T) {
	manager, _, clock, logsDir := newTestManager(t, true)
	start := clock.Now()

	result, err := manager.Login(testPassword, "127.0.0.1:54321")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	requireHexLen(t, "session id", result.SessionID, 16)
	requireHexLen(t, "csrf token", result.CSRFToken, 16)
	if got, want := result.ExpiresAt, start.Add(SessionTTLDefault); !got.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v", got, want)
	}
	if got := manager.SessionCount(); got != 1 {
		t.Fatalf("SessionCount() = %d, want 1", got)
	}

	session, ok := manager.Authenticate(requestWithCookies(result.SessionID, result.CSRFToken))
	if !ok {
		t.Fatal("Authenticate() = false, want true")
	}
	if session.ID != result.SessionID || session.CSRF != result.CSRFToken {
		t.Fatalf("session = %+v, want id/csrf from LoginResult", session)
	}
	if got := manager.CSRFToken(session); got != result.CSRFToken {
		t.Fatalf("CSRFToken(session) = %q, want %q", got, result.CSRFToken)
	}
	if !manager.InitialPasswordPending() {
		t.Fatal("InitialPasswordPending() = false, want true")
	}

	audit := readAudit(t, logsDir)
	if !strings.Contains(audit, "\"event\":\"login_success\"") {
		t.Fatalf("audit missing login_success: %s", audit)
	}
	if strings.Contains(audit, testPassword) {
		t.Fatalf("audit leaked the password: %s", audit)
	}
	if strings.Contains(audit, result.SessionID) || strings.Contains(audit, result.CSRFToken) {
		t.Fatalf("audit leaked a token: %s", audit)
	}
}

func TestLoginWrongPasswordAndLockout(t *testing.T) {
	manager, _, clock, logsDir := newTestManager(t, false)
	const ip = "127.0.0.1"

	for attempt := 1; attempt <= 4; attempt++ {
		_, err := manager.Login("wrong-password", ip)
		apiErr := requireAPIError(t, err, http.StatusUnauthorized, apierr.CodeLoginFailed)
		if apiErr.Details != nil {
			t.Fatalf("LOGIN_FAILED details = %v, want nil", apiErr.Details)
		}
		if _, locked := manager.LoginLocked(ip); locked {
			t.Fatalf("login locked after %d failures, want unlocked", attempt)
		}
	}

	// The fifth failure locks the IP and reports a retry delay.
	_, err := manager.Login("wrong-password", ip)
	apiErr := requireAPIError(t, err, http.StatusLocked, apierr.CodeLoginLocked)
	details, ok := apiErr.Details.(map[string]any)
	if !ok {
		t.Fatalf("LOGIN_LOCKED details = %#v, want map", apiErr.Details)
	}
	if retry, ok := details["retryAfterSeconds"].(int); !ok || retry != 60 {
		t.Fatalf("retryAfterSeconds = %#v, want 60", details["retryAfterSeconds"])
	}
	if retry, locked := manager.LoginLocked(ip); !locked || retry != 60 {
		t.Fatalf("LoginLocked() = (%d, %v), want (60, true)", retry, locked)
	}

	// Even the correct password is rejected while locked.
	_, err = manager.Login(testPassword, ip)
	requireAPIError(t, err, http.StatusLocked, apierr.CodeLoginLocked)

	// A different client is not affected.
	if _, locked := manager.LoginLocked("10.0.0.7"); locked {
		t.Fatal("unrelated client locked")
	}

	// The lock expires after LoginLockFor: the correct password works again.
	clock.Advance(LoginLockForDefault + time.Second)
	result, err := manager.Login(testPassword, ip)
	if err != nil {
		t.Fatalf("Login() after lock expiry error = %v", err)
	}
	if result.SessionID == "" {
		t.Fatal("Login() after lock expiry returned empty session id")
	}
	if manager.SessionCount() != 1 {
		t.Fatalf("SessionCount() = %d, want 1", manager.SessionCount())
	}

	audit := readAudit(t, logsDir)
	for _, event := range []string{"login_failed", "login_locked", "login_success"} {
		if !strings.Contains(audit, "\"event\":\""+event+"\"") {
			t.Fatalf("audit missing %s: %s", event, audit)
		}
	}
	if strings.Contains(audit, "wrong-password") {
		t.Fatalf("audit leaked a password: %s", audit)
	}
}

func TestLoginFailureWindowExpiry(t *testing.T) {
	manager, _, clock, _ := newTestManager(t, false)
	const ip = "127.0.0.1"

	// Four failures, then the 60s window elapses before the next failure: the
	// counter restarts, so the following attempt is not locked yet.
	for i := 0; i < 4; i++ {
		_, err := manager.Login("wrong-password", ip)
		requireAPIError(t, err, http.StatusUnauthorized, apierr.CodeLoginFailed)
	}
	clock.Advance(LoginWindowDefault + time.Second)
	_, err := manager.Login("wrong-password", ip)
	requireAPIError(t, err, http.StatusUnauthorized, apierr.CodeLoginFailed)

	// Three more failures inside the new window are still not enough
	// (4 total: the counter restarted after the window elapsed).
	for i := 0; i < 3; i++ {
		_, err := manager.Login("wrong-password", ip)
		requireAPIError(t, err, http.StatusUnauthorized, apierr.CodeLoginFailed)
	}
	// The 5th failure of the new window locks the IP.
	_, err = manager.Login("wrong-password", ip)
	requireAPIError(t, err, http.StatusLocked, apierr.CodeLoginLocked)
}

func TestLoginSuccessResetsFailures(t *testing.T) {
	manager, _, _, _ := newTestManager(t, false)
	const ip = "127.0.0.1"
	for i := 0; i < 4; i++ {
		_, err := manager.Login("wrong-password", ip)
		requireAPIError(t, err, http.StatusUnauthorized, apierr.CodeLoginFailed)
	}
	if _, err := manager.Login(testPassword, ip); err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	// A successful login clears the history: four more failures are harmless.
	for i := 0; i < 4; i++ {
		_, err := manager.Login("wrong-password", ip)
		requireAPIError(t, err, http.StatusUnauthorized, apierr.CodeLoginFailed)
	}
	if _, locked := manager.LoginLocked(ip); locked {
		t.Fatal("login locked after a successful login reset the counter")
	}
}

func TestLoginBackendFailure(t *testing.T) {
	manager, creds, _, logsDir := newTestManager(t, false)
	creds.verifyErr = errors.New("bcrypt: invalid hash")

	_, err := manager.Login(testPassword, "127.0.0.1")
	requireAPIError(t, err, http.StatusInternalServerError, apierr.CodeInternal)

	audit := readAudit(t, logsDir)
	if strings.Contains(audit, "login_success") {
		t.Fatalf("audit recorded a success for a failed backend: %s", audit)
	}
	if strings.Contains(audit, testPassword) {
		t.Fatalf("audit leaked the password: %s", audit)
	}
}

func TestAuthenticateSlidingExpiry(t *testing.T) {
	manager, _, clock, _ := newTestManager(t, false)
	result, err := manager.Login(testPassword, "127.0.0.1")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	request := requestWithCookies(result.SessionID, result.CSRFToken)

	// Just before the original expiry the session is valid and slides forward.
	clock.Advance(SessionTTLDefault - time.Minute)
	session, ok := manager.Authenticate(request)
	if !ok {
		t.Fatal("Authenticate() before expiry = false, want true")
	}
	wantNewExpiry := clock.Now().Add(SessionTTLDefault)
	if !session.ExpiresAt.Equal(wantNewExpiry) {
		t.Fatalf("slides ExpiresAt = %v, want %v", session.ExpiresAt, wantNewExpiry)
	}

	// Because of the slide, the session survives past the original expiry...
	clock.Advance(2 * time.Minute) // now past CreatedAt+SessionTTL
	if _, ok := manager.Authenticate(request); !ok {
		t.Fatal("Authenticate() after slide = false, want true")
	}
	// ...but not past the most recent renewal (this call slid it again).
	clock.Advance(SessionTTLDefault)
	if _, ok := manager.Authenticate(request); ok {
		t.Fatal("Authenticate() after renewed expiry = true, want false")
	}
	if got := manager.SessionCount(); got != 0 {
		t.Fatalf("SessionCount() = %d, want 0 after expiry", got)
	}
}

func TestAuthenticateRejectsBadCookies(t *testing.T) {
	manager, _, _, _ := newTestManager(t, false)
	if _, ok := manager.Authenticate(httptest.NewRequest(http.MethodGet, "/", nil)); ok {
		t.Fatal("Authenticate() without cookie = true, want false")
	}
	request := requestWithCookies("0123456789abcdef0123456789abcdef", "")
	if _, ok := manager.Authenticate(request); ok {
		t.Fatal("Authenticate() with unknown session = true, want false")
	}
	if _, ok := manager.Authenticate(nil); ok {
		t.Fatal("Authenticate(nil) = true, want false")
	}
	empty := httptest.NewRequest(http.MethodGet, "/", nil)
	empty.AddCookie(&http.Cookie{Name: SessionCookieName, Value: ""})
	if _, ok := manager.Authenticate(empty); ok {
		t.Fatal("Authenticate() with empty cookie = true, want false")
	}
}

func TestLogout(t *testing.T) {
	manager, _, _, logsDir := newTestManager(t, false)
	result, err := manager.Login(testPassword, "192.168.1.5:4000")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if manager.SessionCount() != 1 {
		t.Fatalf("SessionCount() = %d, want 1", manager.SessionCount())
	}
	manager.Logout(result.SessionID)
	if _, ok := manager.Authenticate(requestWithCookies(result.SessionID, result.CSRFToken)); ok {
		t.Fatal("Authenticate() after Logout = true, want false")
	}
	if got := manager.SessionCount(); got != 0 {
		t.Fatalf("SessionCount() = %d, want 0", got)
	}
	// Logout is idempotent.
	manager.Logout(result.SessionID)
	manager.Logout("")

	audit := readAudit(t, logsDir)
	if !strings.Contains(audit, "\"event\":\"logout\"") {
		t.Fatalf("audit missing logout: %s", audit)
	}
	if !strings.Contains(audit, "\"ip\":\"192.168.1.5\"") {
		t.Fatalf("logout audit missing the session client ip: %s", audit)
	}
}

func TestValidateCSRF(t *testing.T) {
	manager, _, _, _ := newTestManager(t, false)
	result, err := manager.Login(testPassword, "127.0.0.1")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	if !manager.ValidateCSRF(requestWithCookies(result.SessionID, result.CSRFToken)) {
		t.Fatal("ValidateCSRF() with matching cookie+header = false, want true")
	}

	cookieOnly := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	cookieOnly.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: result.CSRFToken})
	if manager.ValidateCSRF(cookieOnly) {
		t.Fatal("ValidateCSRF() without header = true, want false")
	}

	headerOnly := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	headerOnly.Header.Set(CSRFHeaderName, result.CSRFToken)
	if manager.ValidateCSRF(headerOnly) {
		t.Fatal("ValidateCSRF() without cookie = true, want false")
	}

	// The comparison itself must be between the two tokens: any matching pair
	// passes the double-submit check, a mismatched one does not.
	fakeToken := NewCSRFToken()
	mismatch := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	mismatch.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: fakeToken})
	mismatch.Header.Set(CSRFHeaderName, NewCSRFToken())
	if manager.ValidateCSRF(mismatch) {
		t.Fatal("ValidateCSRF() with mismatched cookie+header = true, want false")
	}
	if manager.ValidateCSRF(nil) {
		t.Fatal("ValidateCSRF(nil) = true, want false")
	}
}

func TestCookies(t *testing.T) {
	recorder := httptest.NewRecorder()
	SetSessionCookie(recorder, "session-value", 12*time.Hour)
	setCookie := recorder.Header().Get("Set-Cookie")
	for _, want := range []string{"JSESSIONID=session-value", "Path=/", "Max-Age=43200", "HttpOnly", "SameSite=Lax"} {
		if !strings.Contains(setCookie, want) {
			t.Fatalf("session cookie %q missing %q", setCookie, want)
		}
	}
	if strings.Contains(setCookie, "Secure") {
		t.Fatalf("session cookie must not be Secure over HTTP: %q", setCookie)
	}

	recorder = httptest.NewRecorder()
	ClearSessionCookie(recorder)
	if got := recorder.Header().Get("Set-Cookie"); !strings.Contains(got, "Max-Age=0") {
		t.Fatalf("cleared session cookie = %q, want Max-Age=0", got)
	}

	recorder = httptest.NewRecorder()
	SetCSRFCookie(recorder, "csrf-value", 12*time.Hour)
	csrfCookie := recorder.Header().Get("Set-Cookie")
	for _, want := range []string{"XSRF-TOKEN=csrf-value", "Path=/", "Max-Age=43200", "SameSite=Lax"} {
		if !strings.Contains(csrfCookie, want) {
			t.Fatalf("csrf cookie %q missing %q", csrfCookie, want)
		}
	}
	if strings.Contains(csrfCookie, "HttpOnly") {
		t.Fatalf("csrf cookie must be readable by the SPA: %q", csrfCookie)
	}

	recorder = httptest.NewRecorder()
	ClearCSRFCookie(recorder)
	if got := recorder.Header().Get("Set-Cookie"); !strings.Contains(got, "Max-Age=0") {
		t.Fatalf("cleared csrf cookie = %q, want Max-Age=0", got)
	}

	// No-op arguments must not panic or write cookies.
	recorder = httptest.NewRecorder()
	SetSessionCookie(recorder, "", time.Hour)
	SetCSRFCookie(recorder, "", time.Hour)
	SetSessionCookie(nil, "x", time.Hour)
	SetCSRFCookie(nil, "x", time.Hour)
	ClearSessionCookie(nil)
	ClearCSRFCookie(nil)
	if got := recorder.Header().Get("Set-Cookie"); got != "" {
		t.Fatalf("no-op cookie setters wrote %q", got)
	}
}

func TestNewCSRFToken(t *testing.T) {
	seen := make(map[string]struct{}, 64)
	for i := 0; i < 64; i++ {
		token := NewCSRFToken()
		requireHexLen(t, "csrf token", token, 16)
		if _, dup := seen[token]; dup {
			t.Fatalf("NewCSRFToken() produced a duplicate: %q", token)
		}
		seen[token] = struct{}{}
	}
}

func TestChangePassword(t *testing.T) {
	manager, creds, _, logsDir := newTestManager(t, true)
	result, err := manager.Login(testPassword, "127.0.0.1")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	// Validation failures.
	requireAPIError(t, manager.ChangePassword(result.SessionID, "", "new-password-2"), http.StatusBadRequest, apierr.CodeValidation)
	requireAPIError(t, manager.ChangePassword(result.SessionID, testPassword, "short"), http.StatusBadRequest, apierr.CodeValidation)
	requireAPIError(t, manager.ChangePassword(result.SessionID, testPassword, strings.Repeat("x", 257)), http.StatusBadRequest, apierr.CodeValidation)
	requireAPIError(t, manager.ChangePassword(result.SessionID, testPassword, testPassword), http.StatusBadRequest, apierr.CodeValidation)
	// Wrong old password (backend rejects).
	requireAPIError(t, manager.ChangePassword(result.SessionID, "not-the-old-one", "new-password-2"), http.StatusBadRequest, apierr.CodeValidation)
	// Backend I/O failure is still a 400 per the API contract.
	creds.changeErr = errors.New("disk full")
	requireAPIError(t, manager.ChangePassword(result.SessionID, testPassword, "new-password-2"), http.StatusBadRequest, apierr.CodeValidation)
	creds.changeErr = nil

	const newPassword = "new-password-2"
	if err := manager.ChangePassword(result.SessionID, testPassword, newPassword); err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}
	if got := creds.currentPassword(); got != newPassword {
		t.Fatalf("stored password = %q, want the new password", got)
	}
	if manager.InitialPasswordPending() {
		t.Fatal("InitialPasswordPending() = true after a successful change")
	}

	// The old password no longer works; the new one does.
	_, err = manager.Login(testPassword, "127.0.0.1")
	requireAPIError(t, err, http.StatusUnauthorized, apierr.CodeLoginFailed)
	if _, err := manager.Login(newPassword, "127.0.0.1"); err != nil {
		t.Fatalf("Login() with the new password error = %v", err)
	}

	audit := readAudit(t, logsDir)
	if !strings.Contains(audit, "\"event\":\"password_changed\"") {
		t.Fatalf("audit missing password_changed: %s", audit)
	}
	for _, secret := range []string{testPassword, newPassword} {
		if strings.Contains(audit, secret) {
			t.Fatalf("audit leaked a password: %s", audit)
		}
	}
}

func TestChangePasswordInvalidatesOtherSessions(t *testing.T) {
	manager, _, _, _ := newTestManager(t, true)
	current, err := manager.Login(testPassword, "127.0.0.1")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	other, err := manager.Login(testPassword, "10.0.0.9")
	if err != nil {
		t.Fatalf("Login() (other) error = %v", err)
	}
	if manager.SessionCount() != 2 {
		t.Fatalf("SessionCount() = %d, want 2", manager.SessionCount())
	}
	if err := manager.ChangePassword(current.SessionID, testPassword, "new-password-2"); err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}
	if _, ok := manager.Authenticate(requestWithCookies(other.SessionID, other.CSRFToken)); ok {
		t.Fatal("other session still valid after the password change")
	}
	if _, ok := manager.Authenticate(requestWithCookies(current.SessionID, current.CSRFToken)); !ok {
		t.Fatal("the session that changed the password was invalidated")
	}
	if got := manager.SessionCount(); got != 1 {
		t.Fatalf("SessionCount() = %d, want 1", got)
	}
}

func TestSessionTTLConfig(t *testing.T) {
	creds := &fakeCreds{password: testPassword}
	clock := newFakeClock()
	manager := NewManager(Config{SessionTTL: 90 * time.Minute, LoginMaxFailures: 2}, creds, nil, nil)
	manager.now = clock.Now
	if got, want := manager.SessionTTL(), 90*time.Minute; got != want {
		t.Fatalf("SessionTTL() = %v, want %v", got, want)
	}
	result, err := manager.Login(testPassword, "127.0.0.1")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if want := clock.Now().Add(90 * time.Minute); !result.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v", result.ExpiresAt, want)
	}
	// Config.LoginMaxFailures = 2: the second failure locks.
	_, err = manager.Login("wrong-password", "127.0.0.1")
	requireAPIError(t, err, http.StatusUnauthorized, apierr.CodeLoginFailed)
	_, err = manager.Login("wrong-password", "127.0.0.1")
	requireAPIError(t, err, http.StatusLocked, apierr.CodeLoginLocked)
}

func TestNilDependenciesAreSafe(t *testing.T) {
	manager := NewManager(Config{}, nil, nil, nil)
	if manager.InitialPasswordPending() {
		t.Fatal("InitialPasswordPending() with nil credentials = true")
	}
	_, err := manager.Login(testPassword, "")
	requireAPIError(t, err, http.StatusInternalServerError, apierr.CodeInternal)
	requireAPIError(t, manager.ChangePassword("", "a", "b"), http.StatusInternalServerError, apierr.CodeInternal)
	manager.Logout("")
	if manager.SessionCount() != 0 {
		t.Fatal("SessionCount() != 0 for a manager without sessions")
	}
}

func TestNormalizeClientIP(t *testing.T) {
	cases := map[string]string{
		"":                  "unknown",
		"   ":               "unknown",
		"127.0.0.1:1234":    "127.0.0.1",
		"127.0.0.1":         "127.0.0.1",
		"[::1]:8080":        "::1",
		"::1":               "::1",
		"203.0.113.9:65000": "203.0.113.9",
	}
	for raw, want := range cases {
		if got := normalizeClientIP(raw); got != want {
			t.Errorf("normalizeClientIP(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestConcurrentUse(t *testing.T) {
	manager, _, _, _ := newTestManager(t, false)
	var wg sync.WaitGroup
	for worker := 0; worker < 24; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			// Half the workers use the correct password, half a wrong one, and
			// they share a small set of client IPs to exercise the limiter map.
			password := testPassword
			if worker%2 == 1 {
				password = "wrong-password"
			}
			ip := fmt.Sprintf("10.0.0.%d", worker%4)
			result, err := manager.Login(password, ip)
			if err != nil {
				if _, isAPI := apierr.As(err); !isAPI {
					t.Errorf("Login() error is not *apierr.Error: %v", err)
				}
				return
			}
			request := requestWithCookies(result.SessionID, result.CSRFToken)
			for i := 0; i < 5; i++ {
				if _, ok := manager.Authenticate(request); !ok {
					t.Errorf("Authenticate() = false for a fresh session")
					return
				}
				if !manager.ValidateCSRF(request) {
					t.Errorf("ValidateCSRF() = false for matching cookies")
					return
				}
				_ = manager.SessionCount()
				_, _ = manager.LoginLocked(ip)
				_ = manager.CSRFToken(&Session{})
			}
			manager.Logout(result.SessionID)
		}(worker)
	}
	wg.Wait()
	if got := manager.SessionCount(); got != 0 {
		t.Fatalf("SessionCount() = %d after every worker logged out, want 0", got)
	}
}
