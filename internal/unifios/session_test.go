package unifios

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.com/sharkops/exporters/unas-protect-exporter/internal/httpx"
)

// osServer serves the UniFi OS login shape. authOK flips to make /data return
// 401 until a fresh login happens, which is how the re-login path is driven.
func osServer(t *testing.T, logins *int32, dataStatus func(loginCount int32) int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/login":
			atomic.AddInt32(logins, 1)
			w.Header().Set("X-CSRF-Token", "csrf-abc")
			http.SetCookie(w, &http.Cookie{Name: "TOKEN", Value: "t"})
			w.Write([]byte(`{}`))
		case "/data":
			if code := dataStatus(atomic.LoadInt32(logins)); code != http.StatusOK {
				w.WriteHeader(code)
				w.Write([]byte("unauthorized"))
				return
			}
			w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestLoginCapturesCSRFToken(t *testing.T) {
	var logins int32
	srv := osServer(t, &logins, func(int32) int { return http.StatusOK })
	defer srv.Close()

	s, err := New(srv.URL, "u", "p", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Login(context.Background()); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if s.CSRFToken() != "csrf-abc" {
		t.Errorf("CSRFToken = %q, want csrf-abc", s.CSRFToken())
	}
}

func TestGetLogsInOnceThenReusesSession(t *testing.T) {
	var logins int32
	srv := osServer(t, &logins, func(int32) int { return http.StatusOK })
	defer srv.Close()

	s, _ := New(srv.URL, "u", "p", Options{})
	for i := 0; i < 3; i++ {
		if _, err := s.Get(context.Background(), srv.URL+"/data"); err != nil {
			t.Fatalf("Get %d: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(&logins); got != 1 {
		t.Errorf("logins = %d, want 1 (session must be reused)", got)
	}
}

func TestGetReloginsOnceOn401(t *testing.T) {
	var logins int32
	// 401 until a second login has happened.
	srv := osServer(t, &logins, func(n int32) int {
		if n < 2 {
			return http.StatusUnauthorized
		}
		return http.StatusOK
	})
	defer srv.Close()

	s, _ := New(srv.URL, "u", "p", Options{})
	body, err := s.Get(context.Background(), srv.URL+"/data")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Errorf("body = %s", body)
	}
	if got := atomic.LoadInt32(&logins); got != 2 {
		t.Errorf("logins = %d, want 2 (one initial, one retry)", got)
	}
}

func TestGetGivesUpAfterSecond401(t *testing.T) {
	var logins int32
	srv := osServer(t, &logins, func(int32) int { return http.StatusUnauthorized })
	defer srv.Close()

	s, _ := New(srv.URL, "u", "p", Options{})
	if _, err := s.Get(context.Background(), srv.URL+"/data"); err == nil {
		t.Fatal("expected an error after two consecutive 401s")
	}
	if got := atomic.LoadInt32(&logins); got != 2 {
		t.Errorf("logins = %d, want 2 (must not retry forever)", got)
	}
}

// TestConcurrentGetsLoginOnce drives the scenario that motivated single-flight
// auth: one Session is reused across scrapes, and Prometheus is free to scrape
// /metrics from several servers at once, so overlapping first calls into a
// fresh session are routine rather than theoretical — and each one that
// stacked its own login would be a POST /api/auth/login the console has to
// process. N goroutines racing Get on a never-yet-authenticated session must
// still produce exactly one login. Run with -race: authMu/gen are what make
// this safe under the race detector.
func TestConcurrentGetsLoginOnce(t *testing.T) {
	var logins int32
	srv := osServer(t, &logins, func(int32) int { return http.StatusOK })
	defer srv.Close()

	s, err := New(srv.URL, "u", "p", Options{})
	if err != nil {
		t.Fatal(err)
	}

	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Get(context.Background(), srv.URL+"/data"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("Get: %v", err)
	}

	if got := atomic.LoadInt32(&logins); got != 1 {
		t.Errorf("logins = %d, want 1 (concurrent first requests must single-flight the login)", got)
	}
}

// TestConcurrentReauthLogsInOnce drives the harder half of single-flight
// auth: reauth's generation check, not ensureLoggedIn's. Several goroutines
// share one already-logged-in session; all of them hit a 401 at roughly the
// same moment (the session expired server-side), and exactly one of them may
// perform the re-login. The rest must notice — via the gen check in reauth —
// that someone else already refreshed the session, skip logging in again,
// and retry against the fresh session rather than erroring.
func TestConcurrentReauthLogsInOnce(t *testing.T) {
	var logins int32
	// /data returns 401 while only the initial login has happened, and 200
	// once a second login (the re-login) has completed. logins can only ever
	// reach 2: it requires some goroutine to have first observed a 401 (which
	// itself requires logins == 1) and driven reauth, and reauth's gen check
	// stops any other goroutine from logging in a third time. So this design
	// cannot deadlock or livelock — the first 401 anyone sees is guaranteed
	// to trigger the one re-login that then satisfies everyone else.
	srv := osServer(t, &logins, func(n int32) int {
		if n < 2 {
			return http.StatusUnauthorized
		}
		return http.StatusOK
	})
	defer srv.Close()

	s, err := New(srv.URL, "u", "p", Options{})
	if err != nil {
		t.Fatal(err)
	}

	// Prime the session with its initial login via ensureLoggedIn directly,
	// not Get: a Get against /data here would itself observe the logins == 1
	// 401 and drive the very re-login this test wants to isolate, leaving
	// nothing left for the concurrent goroutines below to race over.
	if _, err := s.ensureLoggedIn(context.Background()); err != nil {
		t.Fatalf("priming login: %v", err)
	}
	if got := atomic.LoadInt32(&logins); got != 1 {
		t.Fatalf("logins after priming = %d, want 1", got)
	}

	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body, err := s.Get(context.Background(), srv.URL+"/data")
			if err != nil {
				t.Errorf("Get: %v", err)
				return
			}
			if string(body) != `{"ok":true}` {
				t.Errorf("body = %s", body)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&logins); got != 2 {
		t.Errorf("logins = %d, want 2 (one initial, exactly one re-login)", got)
	}
}

func TestNewRequiresCredentials(t *testing.T) {
	if _, err := New("https://x", "", "", Options{}); err == nil {
		t.Fatal("expected an error without username/password")
	}
}

// TestGetRejectsAnOversizedBody covers the same cap on the session's own read
// path. doGet used to io.LimitReader(32<<20) and return the truncated bytes
// with a nil error, so an oversized bootstrap reached the caller as a JSON
// syntax error rather than as "this response was too big".
func TestGetRejectsAnOversizedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/login" {
			w.Header().Set("X-CSRF-Token", "t")
			return
		}
		w.Write(make([]byte, httpx.MaxResponseBytes+1))
	}))
	defer srv.Close()

	s, err := New(srv.URL, "u", "p", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(context.Background(), srv.URL+"/data"); !errors.Is(err, httpx.ErrResponseTooLarge) {
		t.Fatalf("err = %v, want httpx.ErrResponseTooLarge", err)
	}
}

// TestNewAppliesTheRequestTimeout pins the plumbing that made --http.timeout
// mean anything: before it, Options.Timeout was never set and every request
// was capped at httpx.DefaultTimeout no matter what the flags said.
func TestNewAppliesTheRequestTimeout(t *testing.T) {
	s, err := New("https://unifi.example.com", "u", "p", Options{Timeout: 42 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.HTTPClient().Timeout; got != 42*time.Second {
		t.Errorf("client timeout = %v, want 42s", got)
	}

	def, err := New("https://unifi.example.com", "u", "p", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := def.HTTPClient().Timeout; got != httpx.DefaultTimeout {
		t.Errorf("default client timeout = %v, want %v", got, httpx.DefaultTimeout)
	}
}
