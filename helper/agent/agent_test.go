package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SecondBrainProd/ai-limits/helper/config"
	"github.com/SecondBrainProd/ai-limits/helper/creds"
	"github.com/SecondBrainProd/ai-limits/helper/relayclient"
	"github.com/SecondBrainProd/ai-limits/helper/usage"
	"github.com/SecondBrainProd/ai-limits/internal/snapshot"
)

const marker = "SECRET-TOKEN-MARKER"

var now = time.Unix(1_800_000_000, 0)

type fakeCreds struct {
	tok creds.Token
	err error
}

func (f fakeCreds) Load() (creds.Token, error) { return f.tok, f.err }

type fakeUsage struct {
	body  []byte
	err   error
	calls int
}

func (f *fakeUsage) Fetch(_ context.Context, tok string) ([]byte, error) {
	f.calls++
	return f.body, f.err
}

type fakeRelay struct {
	err   error
	calls int
	got   snapshot.Snapshot
	tok   string
}

func (f *fakeRelay) Put(_ context.Context, tok string, s snapshot.Snapshot) error {
	f.calls++
	f.got, f.tok = s, tok
	return f.err
}

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../../internal/snapshot/testdata/usage-20261022.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newAgent(t *testing.T) (Agent, *fakeUsage, *fakeRelay, *bytes.Buffer) {
	u := &fakeUsage{body: fixture(t)}
	r := &fakeRelay{}
	log := &bytes.Buffer{}
	return Agent{
		Creds:  fakeCreds{tok: creds.Token{Access: marker, ExpiresAt: now.Add(time.Hour)}},
		Usage:  u,
		Relay:  r,
		Config: config.Config{Relay: "https://x", HelperToken: "helper-" + marker},
		Now:    func() time.Time { return now },
		Log:    log,
	}, u, r, log
}

func TestOnceSends(t *testing.T) {
	a, _, r, _ := newAgent(t)
	res, err := a.Once(context.Background())
	if err != nil || res.Skipped != "" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	want, _ := snapshot.FromUsage(fixture(t), now)
	if r.calls != 1 || r.got.T != want.T || len(r.got.Rows) != len(want.Rows) || len(res.Rows) != len(want.Rows) {
		t.Fatalf("calls=%d got=%+v want=%+v", r.calls, r.got, want)
	}
}

func TestOnceNotPaired(t *testing.T) {
	a, u, r, _ := newAgent(t)
	a.Config.HelperToken = ""
	res, err := a.Once(context.Background())
	if err != nil || res.Skipped != "not paired — run: ailimits pair <CODE>" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if r.calls != 0 || u.calls != 0 {
		t.Fatal("must not fetch or put")
	}
}

const expiredMsg = "Claude Code token expired — open Claude Code to refresh it"

func TestOnceTokenExpired(t *testing.T) {
	a, u, r, _ := newAgent(t)
	a.Creds = fakeCreds{tok: creds.Token{Access: marker, ExpiresAt: now.Add(-time.Minute)}}
	res, err := a.Once(context.Background())
	if err != nil || res.Skipped != expiredMsg {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if u.calls != 0 || r.calls != 0 {
		t.Fatal("must not fetch or put")
	}
}

func TestOnceUnauthorized(t *testing.T) {
	a, u, r, _ := newAgent(t)
	u.err = usage.ErrUnauthorized
	res, err := a.Once(context.Background())
	if err != nil || res.Skipped != expiredMsg || r.calls != 0 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestOnceCredsSkips(t *testing.T) {
	for _, e := range []error{creds.ErrNotLoggedIn, creds.ErrKeychainTimeout} {
		a, _, r, _ := newAgent(t)
		a.Creds = fakeCreds{err: e}
		res, err := a.Once(context.Background())
		if err != nil || res.Skipped != e.Error() || r.calls != 0 {
			t.Fatalf("res=%+v err=%v", res, err)
		}
	}
}

func TestPreviewDoesNotSend(t *testing.T) {
	a, _, r, _ := newAgent(t)
	res, err := a.Preview(context.Background())
	if err != nil || len(res.Rows) == 0 || r.calls != 0 {
		t.Fatalf("res=%+v err=%v calls=%d", res, err, r.calls)
	}
	a.Config.HelperToken = ""
	if res, _ := a.Preview(context.Background()); len(res.Rows) == 0 {
		t.Fatal("preview must work when not paired")
	}
}

func TestRunRelayDownNoTightLoop(t *testing.T) {
	a, _, r, log := newAgent(t)
	r.err = errors.New("relay request failed (network error)")
	ticks := make(chan time.Time)
	a.NewTicker = func(time.Duration) (<-chan time.Time, func()) { return ticks, func() {} }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- a.Run(ctx, 3*time.Minute) }()
	for i := 0; i < 3; i++ {
		ticks <- now
	}
	cancel()
	<-done
	if r.calls != 3 {
		t.Fatalf("calls=%d", r.calls)
	}
	if n := strings.Count(log.String(), "\n"); n != 3 {
		t.Fatalf("log lines=%d: %q", n, log.String())
	}
}

func TestLogNeverContainsToken(t *testing.T) {
	cases := map[string]func(*Agent, *fakeUsage, *fakeRelay){
		"relay down":  func(a *Agent, u *fakeUsage, r *fakeRelay) { r.err = errors.New("relay request failed") },
		"unauth":      func(a *Agent, u *fakeUsage, r *fakeRelay) { u.err = usage.ErrUnauthorized },
		"fetch fails": func(a *Agent, u *fakeUsage, r *fakeRelay) { u.err = errors.New("usage request failed (network error)") },
		"not paired":  func(a *Agent, u *fakeUsage, r *fakeRelay) { a.Config.HelperToken = "" },
		"bad body":    func(a *Agent, u *fakeUsage, r *fakeRelay) { u.body = []byte("{" + marker) },
		"ok":          func(a *Agent, u *fakeUsage, r *fakeRelay) {},
	}
	for name, mod := range cases {
		a, u, r, log := newAgent(t)
		mod(&a, u, r)
		ticks := make(chan time.Time)
		a.NewTicker = func(time.Duration) (<-chan time.Time, func()) { return ticks, func() {} }
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error)
		go func() { done <- a.Run(ctx, time.Minute) }()
		ticks <- now
		cancel()
		<-done
		if strings.Contains(log.String(), marker) {
			t.Fatalf("%s: log leaks token: %q", name, log.String())
		}
	}
}

func TestRunShutdownLogsNothing(t *testing.T) {
	a, _, r, log := newAgent(t)
	ticks := make(chan time.Time)
	a.NewTicker = func(time.Duration) (<-chan time.Time, func()) { return ticks, func() {} }
	ctx, cancel := context.WithCancel(context.Background())
	r.err = fmt.Errorf("relay request failed: %w", context.Canceled)
	cancel()
	done := make(chan error)
	go func() { done <- a.Run(ctx, time.Minute) }()
	<-done
	// A tick that races with the cancel must not log either.
	ctx2, cancel2 := context.WithCancel(context.Background())
	a.Relay = &cancelRelay{cancel: cancel2}
	go func() { done <- a.Run(ctx2, time.Minute) }()
	ticks <- now
	<-done
	if log.Len() != 0 {
		t.Fatalf("logged on shutdown: %q", log.String())
	}
}

type cancelRelay struct{ cancel func() }

func (c *cancelRelay) Put(ctx context.Context, _ string, _ snapshot.Snapshot) error {
	c.cancel()
	return fmt.Errorf("relay request failed: %w", ctx.Err())
}

func TestRunUnauthorizedLogsExactMessage(t *testing.T) {
	a, _, r, log := newAgent(t)
	r.err = relayclient.ErrUnauthorized
	ticks := make(chan time.Time)
	a.NewTicker = func(time.Duration) (<-chan time.Time, func()) { return ticks, func() {} }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- a.Run(ctx, time.Minute) }()
	ticks <- now
	cancel()
	<-done
	if !strings.Contains(log.String(), " This computer is no longer paired. Open AI Limits on your watch and run: ailimits pair <CODE>\n") {
		t.Fatalf("%q", log.String())
	}
}

// rateLimitedUsage always answers like Anthropic's 429 without Retry-After.
type rateLimitedUsage struct{ calls *int }

func (u rateLimitedUsage) Fetch(context.Context, string) ([]byte, error) {
	*u.calls++
	return nil, usage.ErrRateLimited
}

func TestRunBacksOffOn429(t *testing.T) {
	calls := 0
	now := time.Unix(1_800_000_000, 0)
	ticks := make(chan time.Time)
	every := 5 * time.Minute
	a := Agent{
		Creds:  fakeCreds{tok: creds.Token{Access: "x"}},
		Usage:  rateLimitedUsage{&calls},
		Relay:  &fakeRelay{},
		Config: config.Config{HelperToken: "h"},
		Now:    func() time.Time { return now },
		Log:    io.Discard,
		NewTicker: func(time.Duration) (<-chan time.Time, func()) {
			return ticks, func() {}
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = a.Run(ctx, every); close(done) }()
	// 9 ticks, 5 minutes apart: calls at t=5 (429 → wait 10m), t=15 (→ 20m), t=35 (→ 30m cap) only.
	for i := 1; i <= 9; i++ {
		now = now.Add(every)
		ticks <- now
	}
	cancel()
	<-done
	if calls != 3 {
		t.Fatalf("usage calls = %d, want 3 (backoff 10m, 20m, 30m)", calls)
	}
}

// seqUsage returns the next error from errs on each call (nil after they run out).
type seqUsage struct {
	errs  []error
	calls *int
}

func (u seqUsage) Fetch(context.Context, string) ([]byte, error) {
	i := *u.calls
	*u.calls++
	if i < len(u.errs) {
		return nil, u.errs[i]
	}
	return nil, usage.ErrRateLimited
}

func TestBackoffAfterShortRetryAfterIsAtLeastTwoCycles(t *testing.T) {
	calls := 0
	now := time.Unix(1_800_000_000, 0)
	ticks := make(chan time.Time)
	every := 5 * time.Minute
	short := usage.RateLimitedFor(20 * time.Second) // 429 with Retry-After: 20
	a := Agent{
		Creds:  fakeCreds{tok: creds.Token{Access: "x"}},
		Usage:  seqUsage{errs: []error{short, usage.ErrRateLimited}, calls: &calls},
		Relay:  &fakeRelay{},
		Config: config.Config{HelperToken: "h"},
		Now:    func() time.Time { return now },
		Log:    io.Discard,
		NewTicker: func(time.Duration) (<-chan time.Time, func()) {
			return ticks, func() {}
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = a.Run(ctx, every); close(done) }()
	// t=5: 429 (20 s) → t=10: 429 no header → wait max(40 s, 10 m) = 10 m → t=15 skipped, t=20 call.
	for i := 1; i <= 3; i++ {
		now = now.Add(every)
		ticks <- now
	}
	cancel()
	<-done
	if calls != 2 {
		t.Fatalf("usage calls after 3 ticks = %d, want 2 (the third tick must be skipped)", calls)
	}
}
