// Package httpx provides small shared helpers for the hand-rolled net/http
// integration clients: building an *http.Client with common options, and running
// a GET request with the usual do/status-check/read/close boilerplate. It keeps
// each client's auth and URL logic in the client while removing the repeated
// plumbing (and the repeated, easy-to-get-wrong TLS transport construction).
package httpx

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultTimeout is used when Options.Timeout is zero.
const DefaultTimeout = 10 * time.Second

// Options configures a client built by New.
type Options struct {
	Timeout time.Duration
	// InsecureSkipVerify disables TLS certificate verification. Several
	// integrations target self-signed self-hosted services (UniFi, Portainer,
	// etc.); this centralizes that opt-in in one reviewed place.
	InsecureSkipVerify bool
	// CookieJar, when set, is used for clients that authenticate via a session
	// cookie (UniFi controller, qBittorrent).
	CookieJar http.CookieJar
}

// New builds an *http.Client with the given options. A zero Timeout falls back
// to DefaultTimeout.
func New(opts Options) *http.Client {
	if opts.Timeout == 0 {
		opts.Timeout = DefaultTimeout
	}
	client := &http.Client{Timeout: opts.Timeout, Jar: opts.CookieJar}
	if opts.InsecureSkipVerify {
		// Even when skipping cert verification for a self-hosted target, still
		// require TLS 1.2+ rather than allowing downgrade to legacy protocols.
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				MinVersion:         tls.VersionTLS12,
			},
		}
	}
	return client
}

// HTTPError is returned by GetBytes for a non-2xx response so callers can inspect
// the status code (e.g. to map 401/403 to a friendlier message) while still
// getting a useful default Error() string.
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Body)
}

// GetBytes performs a GET with the given headers and returns the response body
// for a 2xx status. Non-2xx responses return an *HTTPError. The response body is
// always closed.
func GetBytes(ctx context.Context, client *http.Client, url string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	return body, nil
}
