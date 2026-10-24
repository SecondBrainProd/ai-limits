package main

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SecondBrainProd/ai-limits/helper/agent"
	"github.com/SecondBrainProd/ai-limits/helper/config"
	"github.com/SecondBrainProd/ai-limits/helper/creds"
	"github.com/SecondBrainProd/ai-limits/helper/relayclient"
	"github.com/SecondBrainProd/ai-limits/internal/snapshot"
)

const secret = "SECRET-HELPER-TOKEN"

func TestNormalizeCode(t *testing.T) {
	for in, want := range map[string]string{
		" k7q-4md ": "K7Q4MD", "k7q 4md": "K7Q4MD", "K7Q4MD": "K7Q4MD", "": "",
	} {
		if got := normalizeCode(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestFormatRows(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	rows := []snapshot.Row{
		{Kind: "session", Pct: 7, Reset: now.Add(3*time.Hour + 2*time.Minute + 20*time.Second).Unix()},
		{Kind: "weekly_all", Pct: 72, Reset: now.Add(34 * time.Hour).Unix()},
		{Kind: "weekly_scoped", Label: "Fable", Pct: 99, Reset: now.Add(34 * time.Hour).Unix()},
		{Kind: "extra", Label: "Other", Pct: 5},
		{Kind: "session", Pct: 1, Reset: now.Add(-time.Minute).Unix()},
	}
	want := []string{
		"5-hour session  7%  resets in 3h 2m",
		"week  72%  resets in 1d 10h",
		"Fable (week)  99%  resets in 1d 10h",
		"Other  5%",
		"5-hour session  1%  resets soon",
	}
	got := formatRows(rows, now)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s", strings.Join(got, "\n"))
	}
}

type fakePairer struct {
	calls     []string // helper tokens received
	errs      []error
	token     string
	pcodes    []string
	forgot    []string
	forgetErr error
}

func (f *fakePairer) Forget(_ context.Context, tok string) error {
	f.forgot = append(f.forgot, tok)
	return f.forgetErr
}

func (f *fakePairer) Pair(_ context.Context, code, tok string) (string, error) {
	f.pcodes = append(f.pcodes, code)
	f.calls = append(f.calls, tok)
	i := len(f.calls) - 1
	if i < len(f.errs) && f.errs[i] != nil {
		return "", f.errs[i]
	}
	return f.token, nil
}

func testCLI(t *testing.T, p *fakePairer, ag func(config.Config) agent.Agent) (*cli, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AppData", dir)
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	return &cli{
		out: out, err: errb,
		newRelay: func(config.Config) pairer { return p },
		newAgent: ag,
		now:      func() time.Time { return time.Unix(1_800_000_000, 0) },
		poll:     5 * time.Millisecond,
		every:    time.Hour,
	}, out, errb
}

type okCreds struct{}

func (okCreds) Load() (creds.Token, error) { return creds.Token{Access: "x"}, nil }

type stubUsage struct{ body []byte }

func (s stubUsage) Fetch(context.Context, string) ([]byte, error) { return s.body, nil }

type recRelay struct {
	mu   sync.Mutex
	toks []string
	err  error
}

func (r *recRelay) Put(_ context.Context, tok string, _ snapshot.Snapshot) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.toks = append(r.toks, tok)
	return r.err
}
func (r *recRelay) tokens() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.toks...)
}

var usageBody = []byte(`{"five_hour":{"utilization":7,"resets_at":null},"seven_day":{"utilization":72,"resets_at":null}}`)

func agentWith(rel *recRelay) func(config.Config) agent.Agent {
	return func(cfg config.Config) agent.Agent {
		return agent.Agent{Creds: okCreds{}, Usage: stubUsage{usageBody}, Relay: rel, Config: cfg,
			Now: func() time.Time { return time.Unix(1_800_000_000, 0) }}
	}
}

func TestPairSavesTokenAndSends(t *testing.T) {
	p := &fakePairer{token: secret}
	rel := &recRelay{}
	c, out, errb := testCLI(t, p, agentWith(rel))
	if code := c.run(context.Background(), []string{"pair", "k7q-", "4md"}); code != 0 {
		t.Fatal(code, errb.String())
	}
	if p.pcodes[0] != "K7Q4MD" {
		t.Fatal(p.pcodes)
	}
	if !strings.Contains(out.String(), "Paired. Your watch will show limits within 5 minutes.") {
		t.Fatal(out.String())
	}
	cfg, _ := config.Load()
	if cfg.HelperToken != secret {
		t.Fatal("token not saved")
	}
	if toks := rel.tokens(); len(toks) != 1 || toks[0] != secret {
		t.Fatal("no first send", toks)
	}
	if strings.Contains(out.String()+errb.String(), secret) {
		t.Fatal("token printed")
	}
}

func TestPairBadCode(t *testing.T) {
	p := &fakePairer{errs: []error{relayclient.ErrBadCode}}
	c, out, errb := testCLI(t, p, agentWith(&recRelay{}))
	if code := c.run(context.Background(), []string{"pair", "ZZZZZZ"}); code != 1 {
		t.Fatal(code)
	}
	if !strings.Contains(errb.String(), "That code is invalid or expired. Open AI Limits on your watch for a fresh one.") || out.Len() != 0 {
		t.Fatal(out.String(), errb.String())
	}
	if cfg, _ := config.Load(); cfg.HelperToken != "" {
		t.Fatal("token saved on failure")
	}
}

func TestPairRateLimited(t *testing.T) {
	p := &fakePairer{errs: []error{relayclient.ErrRateLimited}}
	c, _, errb := testCLI(t, p, agentWith(&recRelay{}))
	if code := c.run(context.Background(), []string{"pair", "ZZZZZZ"}); code != 1 || !strings.Contains(errb.String(), "Too many attempts. Wait a minute and try again.") {
		t.Fatal(code, errb.String())
	}
}

func TestPairRetriesWithoutStaleToken(t *testing.T) {
	p := &fakePairer{token: "new-" + secret, errs: []error{relayclient.ErrUnauthorized}}
	c, _, errb := testCLI(t, p, agentWith(&recRelay{}))
	if err := config.Save(config.Config{Relay: config.DefaultRelay, HelperToken: "old-" + secret}); err != nil {
		t.Fatal(err)
	}
	if code := c.run(context.Background(), []string{"pair", "K7Q4MD"}); code != 0 {
		t.Fatal(code, errb.String())
	}
	if len(p.calls) != 2 || p.calls[0] != "old-"+secret || p.calls[1] != "" {
		t.Fatal(p.calls)
	}
	if cfg, _ := config.Load(); cfg.HelperToken != "new-"+secret {
		t.Fatal("new token not saved")
	}
}

func TestPairNoCode(t *testing.T) {
	c, _, errb := testCLI(t, &fakePairer{}, agentWith(&recRelay{}))
	if code := c.run(context.Background(), []string{"pair"}); code != 2 || !strings.Contains(errb.String(), "ailimits pair <CODE>") {
		t.Fatal(code, errb.String())
	}
}

func TestOnceUnauthorizedMessage(t *testing.T) {
	rel := &recRelay{err: relayclient.ErrUnauthorized}
	c, out, errb := testCLI(t, &fakePairer{}, agentWith(rel))
	config.Save(config.Config{Relay: config.DefaultRelay, HelperToken: secret})
	if code := c.run(context.Background(), []string{"once"}); code != 1 {
		t.Fatal(code)
	}
	want := "This computer is no longer paired. Open AI Limits on your watch and run: ailimits pair <CODE>"
	if !strings.Contains(errb.String(), want) || strings.Contains(out.String()+errb.String(), secret) {
		t.Fatal(out.String(), errb.String())
	}
}

func TestStatusPaired(t *testing.T) {
	c, out, _ := testCLI(t, &fakePairer{}, agentWith(&recRelay{}))
	config.Save(config.Config{Relay: config.DefaultRelay, HelperToken: secret})
	if code := c.run(context.Background(), []string{"status"}); code != 0 {
		t.Fatal(code)
	}
	s := out.String()
	if !strings.Contains(s, "Paired: yes") || !strings.Contains(s, "5-hour session  7%") || !strings.Contains(s, "week  72%") || strings.Contains(s, secret) {
		t.Fatal(s)
	}
}

func TestStatusNotPairedAndSkipped(t *testing.T) {
	ag := func(cfg config.Config) agent.Agent {
		return agent.Agent{Creds: failCreds{}, Config: cfg}
	}
	c, out, _ := testCLI(t, &fakePairer{}, ag)
	if code := c.run(context.Background(), []string{"status"}); code != 0 {
		t.Fatal(code)
	}
	s := out.String()
	if !strings.Contains(s, "Paired: no") || !strings.Contains(s, "ailimits pair <CODE>") || !strings.Contains(s, creds.ErrNotLoggedIn.Error()) {
		t.Fatal(s)
	}
}

type failCreds struct{}

func (failCreds) Load() (creds.Token, error) { return creds.Token{}, creds.ErrNotLoggedIn }

func TestUnpair(t *testing.T) {
	c, out, _ := testCLI(t, &fakePairer{}, agentWith(&recRelay{}))
	config.Save(config.Config{Relay: "https://r.example", HelperToken: secret})
	if code := c.run(context.Background(), []string{"unpair"}); code != 0 {
		t.Fatal(code)
	}
	cfg, _ := config.Load()
	if cfg.HelperToken != "" || cfg.Relay != "https://r.example" {
		t.Fatal(cfg.Relay, "token:", cfg.HelperToken != "")
	}
	if strings.Contains(out.String(), secret) {
		t.Fatal("token printed")
	}
}

func TestVersionAndHelp(t *testing.T) {
	version = "1.2.3"
	defer func() { version = "dev" }()
	c, out, _ := testCLI(t, &fakePairer{}, agentWith(&recRelay{}))
	c.run(context.Background(), []string{"version"})
	if out.String() != "ailimits 1.2.3\n" {
		t.Fatal(out.String())
	}
	for _, args := range [][]string{nil, {"help"}} {
		out.Reset()
		if code := c.run(context.Background(), args); code != 0 {
			t.Fatal(code)
		}
		for _, cmd := range []string{"pair", "once", "run", "status", "unpair", "install-service", "uninstall-service", "version"} {
			if !strings.Contains(out.String(), cmd) {
				t.Errorf("usage lacks %s", cmd)
			}
		}
	}
	var errb bytes.Buffer
	c.err = &errb
	if code := c.run(context.Background(), []string{"bogus"}); code != 2 || !strings.Contains(errb.String(), "ailimits help") {
		t.Fatal(code, errb.String())
	}
}

func TestRunSendsThenStopsOnCancelAndPicksUpPairing(t *testing.T) {
	rel := &recRelay{}
	c, _, errb := testCLI(t, &fakePairer{}, agentWith(rel))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int)
	go func() { done <- c.run(ctx, []string{"run"}) }()
	// Not paired yet: the first cycle sends nothing. Pairing later is picked up without a restart.
	time.Sleep(30 * time.Millisecond)
	if len(rel.tokens()) != 0 {
		t.Fatal("sent while unpaired")
	}
	config.Save(config.Config{Relay: config.DefaultRelay, HelperToken: secret})
	deadline := time.After(2 * time.Second)
	for len(rel.tokens()) == 0 {
		select {
		case <-deadline:
			t.Fatal("pairing not picked up")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatal(code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run did not stop")
	}
	if strings.Contains(errb.String(), secret) || strings.Contains(errb.String(), "context canceled") {
		t.Fatal(errb.String())
	}
}

func TestPairUnauthorizedWithoutSavedToken(t *testing.T) {
	p := &fakePairer{errs: []error{relayclient.ErrUnauthorized}}
	c, _, errb := testCLI(t, p, agentWith(&recRelay{}))
	if code := c.run(context.Background(), []string{"pair", "K7Q4MD"}); code != 1 {
		t.Fatal(code)
	}
	if strings.TrimSpace(errb.String()) != "Pairing failed. Try again with a fresh code from your watch." || len(p.calls) != 1 {
		t.Fatal(errb.String(), p.calls)
	}
}

func TestPairHintsInstallServiceWhenNoAutostart(t *testing.T) {
	for _, tc := range []struct {
		name    string
		missing func() bool
		want    bool
	}{
		{"missing", func() bool { return true }, true},
		{"installed", func() bool { return false }, false},
		{"unknown", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &fakePairer{token: secret}
			c, out, errb := testCLI(t, p, agentWith(&recRelay{}))
			c.autostartMissing = tc.missing
			if code := c.run(context.Background(), []string{"pair", "K7Q4MD"}); code != 0 {
				t.Fatal(code, errb.String())
			}
			const hint = "Next: run `ailimits install-service` so limits keep updating."
			if got := strings.Contains(out.String(), hint); got != tc.want {
				t.Fatalf("hint printed = %v, want %v; out=%q", got, tc.want, out.String())
			}
		})
	}
}
