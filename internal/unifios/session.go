// Package unifios provides the UniFi OS console session shared by the
// integrations that talk to a UDM/UniFi OS host. UniFi OS authenticates with a
// cookie session rather than a static header: POST /api/auth/login sets a TOKEN
// cookie and returns an X-CSRF-Token that subsequent requests must echo.
//
// This package deliberately owns ONLY that session. The network-controller
// specifics — API-key auth, the classic /api/login fallback, and the
// /proxy/network path prefix — stay in another-app' UniFi Network client,
// because they have no meaning for the Protect API.
package unifios

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"time"

	"gitlab.com/sharkops/exporters/exporter-common/httpx"
)

// Session holds one authenticated UniFi OS console session.
type Session struct {
	host     string
	username string
	password string

	client *http.Client

	// authMu serializes login attempts. It is never held while a data request
	// is in flight — only ensureLoggedIn/reauth hold it, and both release it
	// before doGet runs — so a slow bootstrap fetch can never block a sibling
	// goroutine's login.
	authMu sync.Mutex

	mu        sync.Mutex
	csrfToken string
	loggedIn  bool
	// gen counts successful logins. It lets reauth tell "the session I used is
	// still the current one" apart from "someone already refreshed it while I
	// was retrying", so concurrent 401s collapse into a single re-login instead
	// of each stacking its own.
	gen uint64
}

// Options configures a Session.
type Options struct {
	// InsecureSkipVerify disables TLS certificate verification. It defaults to
	// FALSE here, unlike most clients in this repo: a UniFi OS console can hold
	// a real certificate (this deployment serves a Let's Encrypt cert on
	// unifi.example.com, verified working against both the login and
	// bootstrap endpoints), and skipping verification on a host that has a valid
	// cert throws away the protection for nothing. Callers pointing at a bare IP
	// with a self-signed cert opt in explicitly.
	InsecureSkipVerify bool
	// Timeout bounds each individual HTTP request (login as well as data
	// requests). Zero falls back to httpx.DefaultTimeout.
	Timeout time.Duration
}

// New builds a Session. The cookie jar is what carries the TOKEN cookie across
// requests.
func New(host, username, password string, opts Options) (*Session, error) {
	if username == "" || password == "" {
		return nil, fmt.Errorf("unifi os session requires a username and password")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &Session{
		host:     strings.TrimRight(host, "/"),
		username: username,
		password: password,
		client: httpx.New(httpx.Options{
			InsecureSkipVerify: opts.InsecureSkipVerify,
			CookieJar:          jar,
			Timeout:            opts.Timeout,
		}),
	}, nil
}

// HTTPClient exposes the cookie-jar client so callers can issue requests that
// need shapes Get does not cover.
func (s *Session) HTTPClient() *http.Client { return s.client }

// CSRFToken returns the token captured at login (empty before the first login).
func (s *Session) CSRFToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.csrfToken
}

// Login performs POST /api/auth/login and captures the CSRF token. It is safe
// to call repeatedly; each call performs an unconditional login and replaces
// the cached session (bumping gen), regardless of whether one is already
// valid.
//
// Login does NOT take authMu and so does NOT serialize with Get: a caller
// that invokes Login directly bypasses the single-flight guarantee that
// ensureLoggedIn/reauth rely on, and can bump gen out from under a concurrent
// reauth in ways that make it skip a re-login it still needed. Callers that
// want single-flight behaviour must go through Get instead.
//
// another-app' UniFi Network client calls Login directly on a Session it
// constructs for itself and never shares with Get, so that bypass doesn't
// matter there — but it would for any session that is also driven through
// Get.
func (s *Session) Login(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{"username": s.username, "password": s.password})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.host+"/api/auth/login", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("unifi os login failed: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	s.mu.Lock()
	s.csrfToken = resp.Header.Get("X-CSRF-Token")
	s.loggedIn = true
	s.gen++
	s.mu.Unlock()
	return nil
}

// ensureLoggedIn returns the generation of a valid login, logging in first if
// this session has never authenticated. Concurrent callers serialize on
// authMu: the loser of the race re-checks loggedIn under s.mu once it gets
// the lock and finds the winner already logged in, so only one Login ever
// happens for a burst of simultaneous first requests.
func (s *Session) ensureLoggedIn(ctx context.Context) (uint64, error) {
	s.mu.Lock()
	if s.loggedIn {
		gen := s.gen
		s.mu.Unlock()
		return gen, nil
	}
	s.mu.Unlock()

	s.authMu.Lock()
	defer s.authMu.Unlock()

	// Re-check: another goroutine may have logged in while we were waiting for
	// authMu.
	s.mu.Lock()
	if s.loggedIn {
		gen := s.gen
		s.mu.Unlock()
		return gen, nil
	}
	s.mu.Unlock()

	if err := s.Login(ctx); err != nil {
		return 0, err
	}
	s.mu.Lock()
	gen := s.gen
	s.mu.Unlock()
	return gen, nil
}

// reauth handles the 401 path. usedGen is the generation the caller's failed
// request was authenticated with. If the current generation has already moved
// past it, some other goroutine already re-logged-in while this caller was in
// flight, so there is nothing to do — the caller retries and picks up that
// fresher session instead of stacking another login on top. Otherwise it
// performs exactly one re-login.
func (s *Session) reauth(ctx context.Context, usedGen uint64) error {
	s.authMu.Lock()
	defer s.authMu.Unlock()

	s.mu.Lock()
	current := s.gen
	s.mu.Unlock()
	if current != usedGen {
		return nil
	}

	s.mu.Lock()
	s.loggedIn = false
	s.mu.Unlock()

	return s.Login(ctx)
}

// ApplyHeaders sets the headers an authenticated UniFi OS request needs.
func (s *Session) ApplyHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/json")
	if tok := s.CSRFToken(); tok != "" {
		req.Header.Set("X-CSRF-Token", tok)
	}
}

// Get issues an authenticated GET, logging in first if this session has never
// authenticated. A 401 means the session expired server-side: re-login once and
// retry. A second 401 is a real failure (bad credentials, revoked access) and
// is returned rather than retried, so a broken account cannot spin.
func (s *Session) Get(ctx context.Context, url string) ([]byte, error) {
	gen, err := s.ensureLoggedIn(ctx)
	if err != nil {
		return nil, err
	}

	body, status, err := s.doGet(ctx, url)
	if err != nil {
		return nil, err
	}
	if status != http.StatusUnauthorized {
		if status != http.StatusOK {
			return nil, fmt.Errorf("HTTP %d: %s", status, strings.TrimSpace(string(body)))
		}
		return body, nil
	}

	// Expired session — re-authenticate (once, even if other goroutines hit
	// the same 401 concurrently) and try exactly once more.
	if err := s.reauth(ctx, gen); err != nil {
		return nil, err
	}
	body, status, err = s.doGet(ctx, url)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func (s *Session) doGet(ctx context.Context, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	s.ApplyHeaders(req)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	// Shared bound with httpx.GetBytes, and an ERROR on overflow rather than a
	// silent truncation: a truncated bootstrap surfaces as a JSON syntax error
	// at an arbitrary offset, which looks like a firmware bug and is not.
	b, err := httpx.ReadBodyLimited(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return b, resp.StatusCode, nil
}
