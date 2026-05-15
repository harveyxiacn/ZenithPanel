package cli

import (
	"bytes"
	"context"
	cryptotls "crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Client is a tiny HTTP wrapper that targets one Profile. It hides the
// distinction between unix-socket and TCP transports — handlers just call
// Get/Post/etc.
type Client struct {
	Profile Profile
	Inner   *http.Client
}

// NewClient builds the right transport for the profile's host scheme.
func NewClient(p Profile) *Client {
	if isUnixHost(p.Host) {
		sock := strings.TrimPrefix(p.Host, "unix://")
		return &Client{
			Profile: p,
			Inner: &http.Client{
				Timeout: 30 * time.Second,
				Transport: &http.Transport{
					DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
						d := net.Dialer{Timeout: 5 * time.Second}
						return d.DialContext(ctx, "unix", sock)
					},
				},
			},
		}
	}
	tlsCfg := &cryptotls.Config{InsecureSkipVerify: !p.VerifyTLS}
	return &Client{
		Profile: p,
		Inner: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: tlsCfg},
		},
	}
}

// effectiveBase returns the URL prefix to glue paths onto. For unix sockets
// the host is a synthetic "unix" — the dialer ignores it anyway.
func (c *Client) effectiveBase() string {
	if isUnixHost(c.Profile.Host) {
		return "http://unix"
	}
	return strings.TrimRight(c.Profile.Host, "/")
}

// Envelope mirrors the panel's success/error response shape.
type Envelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Do issues an HTTP request and returns the decoded envelope plus the raw
// response status. Network errors return ErrTransport so callers can map to
// the documented exit code 4.
func (c *Client) Do(method, path string, body any) (*Envelope, int, error) {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, c.effectiveBase()+path, rdr)
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Profile.Token != "" && !isUnixHost(c.Profile.Host) {
		req.Header.Set("Authorization", "Bearer "+c.Profile.Token)
	}
	req.Header.Set("X-Zenith-Api", "v1")

	resp, err := c.Inner.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %v", ErrTransport, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	env := &Envelope{}
	if len(raw) > 0 {
		jerr := json.Unmarshal(raw, env)
		if jerr != nil {
			// Non-JSON body (sub, backup export, etc.) — pass through raw.
			env.Data = raw
		} else if env.Code == 0 && len(env.Data) == 0 {
			// Valid JSON but not in our envelope shape (e.g. /health).
			// Surface the whole body as data so the caller sees something.
			env.Data = raw
		}
	}
	if env.Code == 0 {
		env.Code = resp.StatusCode
	}
	return env, resp.StatusCode, nil
}

// ErrTransport flags network-level failures.
var ErrTransport = errors.New("transport error")

// Pretty marshals v as indented JSON, used by `--output json` (default).
func Pretty(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
