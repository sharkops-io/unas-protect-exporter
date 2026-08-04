package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewDefaultsTimeout(t *testing.T) {
	c := New(Options{})
	if c.Timeout != DefaultTimeout {
		t.Errorf("Timeout = %v, want %v", c.Timeout, DefaultTimeout)
	}
	if c.Transport != nil {
		t.Error("expected nil Transport when InsecureSkipVerify is false")
	}

	c2 := New(Options{Timeout: 3 * time.Second, InsecureSkipVerify: true})
	if c2.Timeout != 3*time.Second {
		t.Errorf("Timeout = %v, want 3s", c2.Timeout)
	}
	tr, ok := c2.Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify transport")
	}
}

func TestGetBytesSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Test"); got != "yes" {
			t.Errorf("missing header, got %q", got)
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	body, err := GetBytes(context.Background(), New(Options{}), srv.URL, map[string]string{"X-Test": "yes"})
	if err != nil {
		t.Fatalf("GetBytes: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Errorf("body = %s", body)
	}
}

func TestGetBytesNon2xxReturnsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("nope"))
	}))
	defer srv.Close()

	_, err := GetBytes(context.Background(), New(Options{}), srv.URL, nil)
	if err == nil {
		t.Fatal("expected error for 401")
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("expected *HTTPError, got %T", err)
	}
	if httpErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want 401", httpErr.StatusCode)
	}
}
