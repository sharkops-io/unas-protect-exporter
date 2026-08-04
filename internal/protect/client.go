// Package unifi_protect reads the UniFi Protect bootstrap payload from a UniFi
// OS console.
//
// Protect authenticates with a cookie session (see internal/services/unifios),
// NOT the static X-API-Key the UNAS integration uses against
// /proxy/drive/api/v2. One client does not work for both.
package protect

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gitlab.com/sharkops/exporters/unas-protect-exporter/internal/unifios"
)

const bootstrapPath = "/proxy/protect/api/bootstrap"

// Config is the exporter's Protect connection config. Protect authenticates
// with a UniFi OS cookie session, NOT the static X-API-Key the UNAS side uses
// — one client does not work for both.
type Config struct {
	URL                string
	Username           string
	Password           string
	InsecureSkipVerify bool
}

type Client struct {
	host    string
	session *unifios.Session
}

func New(cfg Config) (*Client, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("protect url is required")
	}
	host := strings.TrimRight(cfg.URL, "/")
	session, err := unifios.New(host, cfg.Username, cfg.Password,
		unifios.Options{InsecureSkipVerify: cfg.InsecureSkipVerify})
	if err != nil {
		return nil, err
	}
	return &Client{host: host, session: session}, nil
}

// GetBootstrap fetches and decodes the Protect bootstrap payload.
func (c *Client) GetBootstrap(ctx context.Context) (*Bootstrap, error) {
	body, err := c.session.Get(ctx, c.host+bootstrapPath)
	if err != nil {
		return nil, fmt.Errorf("protect bootstrap: %w", err)
	}
	var bs Bootstrap
	if err := json.Unmarshal(body, &bs); err != nil {
		return nil, fmt.Errorf("decode protect bootstrap: %w", err)
	}
	return &bs, nil
}
