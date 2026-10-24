// Package agent is the helper loop: read the local Claude Code token, fetch
// usage, push a snapshot to the relay. Tokens never reach the log or errors.
package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/SecondBrainProd/ai-limits/helper/config"
	"github.com/SecondBrainProd/ai-limits/helper/creds"
	"github.com/SecondBrainProd/ai-limits/helper/relayclient"
	"github.com/SecondBrainProd/ai-limits/helper/usage"
	"github.com/SecondBrainProd/ai-limits/internal/snapshot"
)

const (
	msgNotPaired = "not paired — run: ailimits pair <CODE>"
	msgExpired   = "Claude Code token expired — open Claude Code to refresh it"
)

// Result describes one cycle. Skipped is the reason nothing was sent ("" if sent).
type Result struct {
	Rows    []snapshot.Row
	Skipped string
}

// Agent wires the pieces together.
type Agent struct {
	Creds interface{ Load() (creds.Token, error) }
	Usage interface {
		Fetch(context.Context, string) ([]byte, error)
	}
	Relay interface {
		Put(context.Context, string, snapshot.Snapshot) error
	}
	Config config.Config
	Now    func() time.Time
	Log    io.Writer
	// NewTicker is optional (nil uses time.NewTicker); it lets tests drive Run.
	NewTicker func(time.Duration) (<-chan time.Time, func())
}

func (a Agent) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// collect loads the token, fetches usage and builds the snapshot.
// A non-empty skipped means "nothing to send, and why".
func (a Agent) collect(ctx context.Context) (snap snapshot.Snapshot, skipped string, err error) {
	tok, err := a.Creds.Load()
	if errors.Is(err, creds.ErrNotLoggedIn) || errors.Is(err, creds.ErrKeychainTimeout) {
		return snap, err.Error(), nil
	}
	if err != nil {
		return snap, "", err
	}
	now := a.now()
	if tok.Expired(now) {
		return snap, msgExpired, nil
	}
	body, err := a.Usage.Fetch(ctx, tok.Access)
	if errors.Is(err, usage.ErrUnauthorized) {
		return snap, msgExpired, nil
	}
	if err != nil {
		return snap, "", err
	}
	snap, err = snapshot.FromUsage(body, now)
	if err != nil {
		// Drop the parser error: it may quote the response body.
		return snap, "", errors.New("cannot parse the usage response")
	}
	return snap, "", nil
}

// Preview reads and parses usage without contacting the relay.
func (a Agent) Preview(ctx context.Context) (Result, error) {
	snap, skipped, err := a.collect(ctx)
	if err != nil || skipped != "" {
		return Result{Skipped: skipped}, err
	}
	return Result{Rows: snap.Rows}, nil
}

// Once runs one cycle: Preview plus the relay upload.
func (a Agent) Once(ctx context.Context) (Result, error) {
	if a.Config.HelperToken == "" {
		return Result{Skipped: msgNotPaired}, nil
	}
	snap, skipped, err := a.collect(ctx)
	if err != nil || skipped != "" {
		return Result{Skipped: skipped}, err
	}
	if err := a.Relay.Put(ctx, a.Config.HelperToken, snap); err != nil {
		return Result{Rows: snap.Rows}, err
	}
	return Result{Rows: snap.Rows}, nil
}

// maxBackoff caps the pause after repeated 429s from Anthropic.
const maxBackoff = 30 * time.Minute

// Run calls Once every `every` until ctx is done. It does not run immediately
// (the caller may call Once first). A failed cycle logs one line and waits for
// the next tick: no accelerated retries.
func (a Agent) Run(ctx context.Context, every time.Duration) error {
	var ch <-chan time.Time
	var stop func()
	if a.NewTicker != nil {
		ch, stop = a.NewTicker(every)
	} else {
		t := time.NewTicker(every)
		ch, stop = t.C, t.Stop
	}
	defer stop()
	// After a 429 from Anthropic, skip ticks: wait what it asked for, otherwise
	// double the pause (2×every, 4×every, …) up to maxBackoff.
	var wait time.Duration
	var next time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ch:
			if !next.IsZero() && a.now().Before(next) {
				continue
			}
			res, err := a.Once(ctx)
			if errors.Is(err, usage.ErrRateLimited) {
				switch ra := usage.RetryAfter(err); {
				case ra > 0:
					wait = ra
				case wait == 0:
					wait = 2 * every
				default:
					// Never shorter than two cycles, even after a short Retry-After.
					wait = max(wait*2, 2*every)
				}
				if wait > maxBackoff {
					wait = maxBackoff
				}
				next = a.now().Add(wait)
				a.logf("Anthropic is rate-limiting usage requests; next try in %s", wait.Round(time.Minute))
				continue
			}
			wait, next = 0, time.Time{}
			switch {
			case err != nil && ctx.Err() != nil && errors.Is(err, ctx.Err()):
				// Shutdown mid-cycle is not an error.
				return nil
			case errors.Is(err, relayclient.ErrUnauthorized):
				a.logf("%s", err)
			case err != nil:
				a.logf("error: %v", err)
			case res.Skipped != "":
				a.logf("skipped: %s", res.Skipped)
			default:
				a.logf("sent")
			}
		}
	}
}

func (a Agent) logf(format string, args ...any) {
	if a.Log == nil {
		return
	}
	fmt.Fprintf(a.Log, a.now().UTC().Format("2006-01-02T15:04:05Z")+" "+format+"\n", args...)
}
