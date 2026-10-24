// Package usage fetches the Claude subscription usage limits. The access token
// is only ever sent in the Authorization header of the request to BaseURL and
// never appears in returned errors.
package usage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// UserAgentPrefix starts every User-Agent the helper sends.
const UserAgentPrefix = "ailimits/"

const (
	defaultBaseURL = "https://api.anthropic.com"
	repoURL        = "https://github.com/SecondBrainProd/ai-limits"
	maxBody        = 1 << 20
)

// ErrRateLimited means Anthropic answered 429: the caller should wait (see RetryAfter).
var ErrRateLimited = errors.New("Anthropic is rate-limiting usage requests")

type rateLimitError struct{ after time.Duration }

func (e rateLimitError) Error() string        { return ErrRateLimited.Error() }
func (e rateLimitError) Is(target error) bool { return target == ErrRateLimited }

// RateLimitedFor is the error Fetch returns for a 429 with Retry-After d.
func RateLimitedFor(d time.Duration) error { return rateLimitError{after: d} }

// RetryAfter returns the wait Anthropic asked for in a 429's Retry-After header
// (seconds), or 0 when it gave none.
func RetryAfter(err error) time.Duration {
	var rl rateLimitError
	if errors.As(err, &rl) {
		return rl.after
	}
	return 0
}

// ErrUnauthorized means Claude rejected the token.
var ErrUnauthorized = errors.New("Claude rejected the token (expired or revoked)")

// Client fetches usage. Zero BaseURL and HTTP use the real endpoint and a 20s-timeout client.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	Version string
}

// Fetch returns the raw usage JSON body.
func (c Client) Fetch(ctx context.Context, accessToken string) ([]byte, error) {
	base := c.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	u, err := url.Parse(base)
	if err != nil || !allowedBase(u) {
		return nil, errors.New("refusing to send the token: usage endpoint must be https")
	}
	// Never follow redirects: the bearer token must only go to the configured host.
	var hc *http.Client
	if c.HTTP == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	} else {
		clone := *c.HTTP
		hc = &clone
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/oauth/usage", nil)
	if err != nil {
		return nil, errors.New("cannot build usage request")
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("User-Agent", UserAgentPrefix+c.Version+" (+"+repoURL+")")
	resp, err := hc.Do(req)
	if err != nil {
		// Report only the cause class, never the wrapped error text (it carries the URL, not the token, but stay strict).
		if ctx.Err() != nil {
			return nil, fmt.Errorf("usage request failed: %w", ctx.Err())
		}
		return nil, errors.New("usage request failed (network error)")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, errors.New("unexpected redirect from usage endpoint")
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		after := time.Duration(0)
		if n, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && n > 0 {
			after = time.Duration(n) * time.Second
		}
		return nil, rateLimitError{after: after}
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("usage request failed: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, errors.New("usage response could not be read")
	}
	return body, nil
}

// allowedBase permits https, and plain http only on a loopback host (for tests).
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
