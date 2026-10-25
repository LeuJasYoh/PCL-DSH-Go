// SPDX-License-Identifier: GPL-3.0-or-later

// Package security implements the single-administrator authentication layer of
// the manager: in-memory sessions with sliding expiry, login rate limiting by
// client IP, CSRF double-submit cookies, password change and audit events.
//
// Contract: docs/M1_INTERFACES.md 5, docs/IMPLEMENTATION_SPEC.md 4.1/5.
//
// Design notes:
//   - Sessions and login attempts live in memory only. Restarting the manager
//     logs every administrator out, which is acceptable for the single-admin
//     deployment model of this milestone.
//   - Passwords are only ever handled by the injected Credentials
//     implementation (store.AdminStore uses BCrypt). This package never logs,
//     stores or returns a password.
//   - Every expected failure is an *apierr.Error so the API can map it to the
//     documented JSON error body without translation.
package security

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/logging"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/passhash"
)

// SessionTTLDefault is the sliding session lifetime (12h, docs/IMPLEMENTATION_SPEC.md 4.1).
const SessionTTLDefault = 12 * time.Hour

// Login limiter defaults (docs/IMPLEMENTATION_SPEC.md 5.2): at most 5 failed
// attempts within 60s lock the client IP for 60s.
const (
	LoginMaxFailuresDefault = 5
	LoginWindowDefault      = 60 * time.Second
	LoginLockForDefault     = 60 * time.Second
)

// Password policy is defined in internal/passhash.

// cleanupInterval throttles the periodic sweep of expired sessions/failures.
const cleanupInterval = time.Minute

// Config configures a Manager. Zero values fall back to the documented
// defaults, so Config{} is a valid configuration.
type Config struct {
	SessionTTL       time.Duration // default 12h (sliding)
	LoginMaxFailures int           // default 5
	LoginWindow      time.Duration // default 60s
	LoginLockFor     time.Duration // default 60s
}

// withDefaults returns cfg with all zero/negative values replaced.
func (c Config) withDefaults() Config {
	if c.SessionTTL <= 0 {
		c.SessionTTL = SessionTTLDefault
	}
	if c.LoginMaxFailures <= 0 {
		c.LoginMaxFailures = LoginMaxFailuresDefault
	}
	if c.LoginWindow <= 0 {
		c.LoginWindow = LoginWindowDefault
	}
	if c.LoginLockFor <= 0 {
		c.LoginLockFor = LoginLockForDefault
	}
	return c
}

// Credentials is the password backend. store.AdminStore implements it; tests
// provide an in-memory fake. The interface intentionally lives here so that
// this package never imports internal/store.
type Credentials interface {
	// VerifyPassword reports whether plain matches the stored BCrypt hash.
	VerifyPassword(plain string) (bool, error)
	// ChangePassword validates oldPassword and persists newPassword.
	ChangePassword(oldPassword, newPassword string) error
	// InitialPasswordPending reports whether the initial password is still in
	// use (first login has not changed it yet).
	InitialPasswordPending() bool
}

// Session is one authenticated administrator session.
type Session struct {
	ID        string
	CSRF      string
	CreatedAt time.Time
	ExpiresAt time.Time

	// ip is the client IP that created the session. It is kept so that audit
	// events for actions without an explicit client IP (logout, password
	// change) can still carry one; it is never serialized.
	ip string
}

// LoginResult is the outcome of a successful login.
type LoginResult struct {
	SessionID string
	CSRFToken string
	ExpiresAt time.Time
}

// attempt tracks failed logins for one client IP.
type attempt struct {
	count       int
	windowStart time.Time
	lockedUntil time.Time
}

// Manager implements the authentication state of the manager. It is safe for
// concurrent use.
type Manager struct {
	cfg    Config
	creds  Credentials
	audit  *logging.Audit
	logger *slog.Logger

	// now is the clock, injectable for tests.
	now func() time.Time

	// loginMu serialises password verification so that concurrent attempts
	// cannot slip past the failure counter between the check and the record.
	// It is held across the BCrypt comparison (tens of milliseconds); that is
	// acceptable because a single administrator logs in rarely.
	loginMu sync.Mutex

	mu          sync.Mutex // guards sessions and failures
	sessions    map[string]*Session
	failures    map[string]*attempt
	lastCleanup time.Time
}

// NewManager builds a Manager. creds must be non-nil for login/change-password
// to work; audit and logger may be nil (they become no-ops).
func NewManager(cfg Config, creds Credentials, audit *logging.Audit, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Manager{
		cfg:      cfg.withDefaults(),
		creds:    creds,
		audit:    audit,
		logger:   logger,
		now:      time.Now,
		sessions: make(map[string]*Session),
		failures: make(map[string]*attempt),
	}
}

// SessionTTL returns the configured sliding session lifetime. The API layer
// uses it for cookie Max-Age values.
func (m *Manager) SessionTTL() time.Duration { return m.cfg.SessionTTL }

// InitialPasswordPending reports whether the initial password still has to be
// changed. It is safe to call before any session exists.
func (m *Manager) InitialPasswordPending() bool {
	if m.creds == nil {
		return false
	}
	return m.creds.InitialPasswordPending()
}

// Login verifies password and, on success, creates a fresh session for ip.
// The caller is responsible for invalidating any previous session of the same
// client (the API layer logs the old JSESSIONID out before calling Login).
//
// Errors:
//   - *apierr.Error 401 LOGIN_FAILED on a wrong password;
//   - *apierr.Error 423 LOGIN_LOCKED with details.retryAfterSeconds while the
//     client IP is locked or when this failure reaches the limit;
//   - *apierr.Error 500 INTERNAL for credential backend failures (logged).
func (m *Manager) Login(password, clientIP string) (LoginResult, error) {
	if m.creds == nil {
		return LoginResult{}, apierr.New(http.StatusInternalServerError, apierr.CodeInternal, "认证后端不可用。")
	}
	ip := normalizeClientIP(clientIP)

	// Serialise attempts from all IPs: the limiter must see failures in order.
	m.loginMu.Lock()
	defer m.loginMu.Unlock()

	if retry, locked := m.lockStatus(ip, m.now()); locked {
		m.auditEvent("login_locked", ip, "retryAfterSeconds", retry)
		return LoginResult{}, lockedError(retry)
	}

	ok, err := m.creds.VerifyPassword(password)
	if err != nil {
		// Never log the password or the hash: only the backend error text.
		m.logger.Error("security: verify password failed", "error", err)
		return LoginResult{}, apierr.New(http.StatusInternalServerError, apierr.CodeInternal, "登录校验失败，请查看服务端日志。")
	}
	now := m.now()
	if !ok {
		locked, retry := m.recordFailure(ip, now)
		m.auditEvent("login_failed", ip)
		if locked {
			m.auditEvent("login_locked", ip, "retryAfterSeconds", retry)
			return LoginResult{}, lockedError(retry)
		}
		return LoginResult{}, apierr.New(http.StatusUnauthorized, apierr.CodeLoginFailed, "密码错误，请重新输入。")
	}

	session, err := m.newSession(ip, now)
	if err != nil {
		m.logger.Error("security: create session failed", "error", err)
		return LoginResult{}, apierr.New(http.StatusInternalServerError, apierr.CodeInternal, "创建会话失败，请查看服务端日志。")
	}
	m.clearFailures(ip)
	m.auditEvent("login_success", ip)
	return LoginResult{
		SessionID: session.ID,
		CSRFToken: session.CSRF,
		ExpiresAt: session.ExpiresAt,
	}, nil
}

// lockStatus reports whether ip is currently locked, together with the
// remaining lock time in whole seconds (rounded up to at least one).
func (m *Manager) lockStatus(ip string, now time.Time) (int, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lockStatusLocked(ip, now)
}

func (m *Manager) lockStatusLocked(ip string, now time.Time) (int, bool) {
	state := m.failures[ip]
	if state == nil || state.lockedUntil.IsZero() {
		return 0, false
	}
	if state.lockedUntil.After(now) {
		return secondsUntil(now, state.lockedUntil), true
	}
	// The lock has expired: restart the failure window on the next attempt.
	state.lockedUntil = time.Time{}
	state.count = 0
	state.windowStart = now
	return 0, false
}

// LoginLocked reports the remaining lock time for clientIP, if any.
func (m *Manager) LoginLocked(clientIP string) (retryAfterSeconds int, locked bool) {
	return m.lockStatus(normalizeClientIP(clientIP), m.now())
}

// recordFailure counts one failed attempt and reports whether it locked the IP.
func (m *Manager) recordFailure(ip string, now time.Time) (locked bool, retryAfter int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.failures[ip]
	if state == nil {
		state = &attempt{}
		m.failures[ip] = state
	}
	if state.windowStart.IsZero() || now.Sub(state.windowStart) > m.cfg.LoginWindow {
		state.count = 0
		state.windowStart = now
	}
	state.count++
	if state.count >= m.cfg.LoginMaxFailures {
		state.lockedUntil = now.Add(m.cfg.LoginLockFor)
		return true, secondsUntil(now, state.lockedUntil)
	}
	return false, 0
}

// clearFailures forgets the failure history of ip after a successful login.
func (m *Manager) clearFailures(ip string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.failures, ip)
}

// newSession creates and registers a session for ip.
func (m *Manager) newSession(ip string, now time.Time) (*Session, error) {
	id, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	csrf, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	session := &Session{
		ID:        id,
		CSRF:      csrf,
		CreatedAt: now,
		ExpiresAt: now.Add(m.cfg.SessionTTL),
		ip:        ip,
	}
	m.mu.Lock()
	m.cleanupLocked(now)
	m.sessions[id] = session
	m.mu.Unlock()
	return session, nil
}

// Logout invalidates sessionID. It is idempotent and records a logout audit
// event carrying the session's client IP when the session was still known.
func (m *Manager) Logout(sessionID string) {
	if sessionID == "" {
		return
	}
	m.mu.Lock()
	session, ok := m.sessions[sessionID]
	if ok {
		delete(m.sessions, sessionID)
	}
	m.mu.Unlock()
	ip := ""
	if ok {
		ip = session.ip
	}
	m.auditEvent("logout", ip)
}

// Authenticate returns the session referenced by the request's JSESSIONID
// cookie, sliding its expiry forward. Expired or unknown sessions are reported
// as unauthenticated (and are pruned).
func (m *Manager) Authenticate(r *http.Request) (*Session, bool) {
	if r == nil {
		return nil, false
	}
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil, false
	}
	return m.session(cookie.Value)
}

// session looks up and slides one session by id.
func (m *Manager) session(id string) (*Session, bool) {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupLocked(now)
	stored, ok := m.sessions[id]
	if !ok {
		return nil, false
	}
	if !stored.ExpiresAt.After(now) {
		delete(m.sessions, id)
		return nil, false
	}
	stored.ExpiresAt = now.Add(m.cfg.SessionTTL)
	clone := *stored
	return &clone, true
}

// SessionCount returns the number of live (non-expired) sessions.
func (m *Manager) SessionCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupLocked(m.now())
	return len(m.sessions)
}

// cleanupLocked prunes expired sessions and stale login attempts. It is cheap
// and throttled by cleanupInterval.
func (m *Manager) cleanupLocked(now time.Time) {
	if !m.lastCleanup.IsZero() && now.Sub(m.lastCleanup) < cleanupInterval {
		return
	}
	m.lastCleanup = now
	for id, session := range m.sessions {
		if !session.ExpiresAt.After(now) {
			delete(m.sessions, id)
		}
	}
	for ip, state := range m.failures {
		if state.lockedUntil.After(now) {
			continue
		}
		if !state.windowStart.IsZero() && now.Sub(state.windowStart) <= m.cfg.LoginWindow {
			continue
		}
		delete(m.failures, ip)
	}
}

// ChangePassword validates oldPassword/newPassword, persists the new password
// through the Credentials backend and records a password_changed audit event.
// Validation failures are *apierr.Error 400 VALIDATION.
func (m *Manager) ChangePassword(sessionID, oldPassword, newPassword string) error {
	if m.creds == nil {
		return apierr.New(http.StatusInternalServerError, apierr.CodeInternal, "认证后端不可用。")
	}
	if oldPassword == "" {
		return apierr.Validation("当前密码不能为空。")
	}
	if err := passhash.Validate(newPassword); err != nil {
		return apierr.New(http.StatusBadRequest, apierr.CodeValidation, err.Error())
	}
	if newPassword == oldPassword {
		return apierr.Validation("新密码不能与当前密码相同。")
	}
	if err := m.creds.ChangePassword(oldPassword, newPassword); err != nil {
		// The backend error may be a validation failure (wrong old password,
		// policy) or an I/O failure; the API contract only exposes 400 here.
		m.logger.Warn("security: change password rejected", "error", err)
		return apierr.Validation("旧密码不正确或新密码不符合要求。")
	}
	ip := m.sessionIP(sessionID)
	// Changing the password invalidates every other session (stolen or stale
	// cookies cannot survive a password rotation); the session that performed
	// the change stays valid so the SPA does not have to log in again.
	m.invalidateOtherSessions(sessionID)
	m.auditEvent("password_changed", ip)
	return nil
}

// invalidateOtherSessions removes every session except keepID.
func (m *Manager) invalidateOtherSessions(keepID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range m.sessions {
		if id != keepID {
			delete(m.sessions, id)
		}
	}
}

// sessionIP returns the client IP recorded for sessionID ("" if unknown).
func (m *Manager) sessionIP(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if session, ok := m.sessions[sessionID]; ok {
		return session.ip
	}
	return ""
}

// auditEvent appends one audit event. An empty ip is omitted so a stray event
// never carries a misleading empty attribute. Sensitive key values are masked
// by logging.Audit itself.
func (m *Manager) auditEvent(event, ip string, attrs ...any) {
	if m.audit == nil {
		return
	}
	all := make([]any, 0, len(attrs)+2)
	if ip != "" {
		all = append(all, "ip", ip)
	}
	all = append(all, attrs...)
	m.audit.Event(event, all...)
}

// lockedError builds the documented 423 LOGIN_LOCKED error.
func lockedError(retryAfterSeconds int) *apierr.Error {
	return apierr.New(http.StatusLocked, apierr.CodeLoginLocked, "登录失败次数过多，请稍后再试。").
		WithDetails(map[string]any{"retryAfterSeconds": retryAfterSeconds})
}

// secondsUntil returns the remaining whole seconds, rounded up and at least 1.
func secondsUntil(now, until time.Time) int {
	remaining := until.Sub(now)
	if remaining <= 0 {
		return 1
	}
	seconds := int((remaining + time.Second - 1) / time.Second)
	if seconds < 1 {
		return 1
	}
	return seconds
}

// normalizeClientIP normalises a RemoteAddr-like value ("ip:port" or "ip")
// into a comparable limiter key. Empty input becomes "unknown" so that
// malformed requests share one bucket instead of bypassing the limiter.
func normalizeClientIP(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "unknown"
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.Trim(value, "[]")
	if value == "" {
		return "unknown"
	}
	return value
}

// randomHex returns n random bytes encoded as 2n lowercase hex characters.
func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("读取随机数失败: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
