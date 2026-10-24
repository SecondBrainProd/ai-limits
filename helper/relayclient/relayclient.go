// Package relayclient talks to the AI Limits relay. The helper token is a
// secret: it only goes in the Authorization header and never appears in errors.
package relayclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/SecondBrainProd/ai-limits/internal/snapshot"
)

// ErrBadCode means the pairing code is wrong or expired.
var ErrBadCode = errors.New("invalid or expired pairing code")

// ErrUnauthorized means the relay does not know the helper token.
var ErrUnauthorized = errors.New("This computer is no longer paired. Open AI Limits on your watch and run: ailimits pair <CODE>")

// ErrRateLimited means the relay answered HTTP 429 to a pairing attempt.
var ErrRateLimited = errors.New("Too many attempts. Wait a minute and try again.")

const maxResp = 4 << 10

// Client is a relay client. Zero HTTP uses a 20s-timeout client.
type Client struct {
	Base      string
	HTTP      *http.Client
	UserAgent string
}

// Pair exchanges a pairing code for a helper token. helperToken, if non-empty,
// re-pairs an existing helper.
func (c Client) Pair(ctx context.Context, code, helperToken string) (string, error) {
	body, _ := json.Marshal(map[string]string{"code": code})
	resp, err := c.do(ctx, http.MethodPost, "/v1/pair", helperToken, body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return "", ErrBadCode
	case resp.StatusCode == http.StatusUnauthorized:
		return "", ErrUnauthorized
	case resp.StatusCode == http.StatusTooManyRequests:
		return "", ErrRateLimited
	case resp.StatusCode != http.StatusOK:
		return "", fmt.Errorf("pairing failed: HTTP %d", resp.StatusCode)
	}
	var out struct {
		HelperToken string `json:"helper_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResp)).Decode(&out); err != nil || out.HelperToken == "" {
		return "", errors.New("pairing response is malformed")
	}
	return out.HelperToken, nil
}

// Put uploads a snapshot.
func (c Client) Put(ctx context.Context, helperToken string, s snapshot.Snapshot) error {
	body, err := json.Marshal(s)
	if err != nil {
		return errors.New("cannot encode snapshot")
	}
	resp, err := c.do(ctx, http.MethodPut, "/v1/snapshot", helperToken, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusTooManyRequests:
		return errors.New("relay is rate-limiting requests")
	}
	return fmt.Errorf("snapshot upload failed: HTTP %d", resp.StatusCode)
}

func (c Client) do(ctx context.Context, method, path, helperToken string, body []byte) (*http.Response, error) {
	u, err := url.Parse(c.Base)
	if err != nil || !allowedBase(u) {
		return nil, errors.New("refusing to contact relay: address must be https")
	}
	var hc *http.Client
	if c.HTTP == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	} else {
		clone := *c.HTTP
		hc = &clone
	}
	// Never follow redirects: the helper token must only go to the configured host.
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, method, u.Scheme+"://"+u.Host+path, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("cannot build relay request")
	}
	req.Header.Set("Content-Type", "application/json")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	if helperToken != "" {
		req.Header.Set("Authorization", "Bearer "+helperToken)
	}
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("relay request failed: %w", ctx.Err())
		}
		return nil, errors.New("relay request failed (network error)")
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		resp.Body.Close()
		return nil, errors.New("unexpected redirect from relay")
	}
	return resp, nil
}

func allowedBase(u *url.URL) bool {
	if u.Scheme == "https" {
		return u.Host != ""
	}
	if u.Scheme == "http" {
		switch u.Hostname() {
		case "127.0.0.1", "::1", "localhost":
			return true
		}
	}
	return false
}

// Forget asks the relay to drop this helper (`ailimits unpair`): its snapshot is
// deleted and its watches show a fresh pairing code. 401 means already forgotten.
func (c Client) Forget(ctx context.Context, helperToken string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/v1/helper", helperToken, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return ErrUnauthorized
	}
	return fmt.Errorf("relay answered HTTP %d", resp.StatusCode)
}
