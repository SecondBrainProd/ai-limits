package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SecondBrainProd/ai-limits/helper/config"
)

func TestParseRunArgs(t *testing.T) {
	dir, err := parseRunArgs([]string{"--claude-config-dir", "/x/.claude"})
	if err != nil || dir != "/x/.claude" {
		t.Fatalf("%q %v", dir, err)
	}
	if dir, err := parseRunArgs(nil); err != nil || dir != "" {
		t.Fatalf("%q %v", dir, err)
	}
	if _, err := parseRunArgs([]string{"--claude-config-dir"}); err == nil {
		t.Fatal("missing value accepted")
	}
	if _, err := parseRunArgs([]string{"--bogus"}); err == nil {
		t.Fatal("unknown flag accepted")
	}
}

func TestUnpairCallsRelay(t *testing.T) {
	f := &fakePairer{}
	c, out, _ := testCLI(t, f, agentWith(&recRelay{}))
	config.Save(config.Config{Relay: "https://r.example", HelperToken: secret})
	if code := c.run(context.Background(), []string{"unpair"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(f.forgot) != 1 || f.forgot[0] != secret {
		t.Fatalf("Forget calls = %d", len(f.forgot))
	}
	cfg, _ := config.Load()
	if !strings.Contains(out.String(), "Unpaired.") || cfg.HelperToken != "" {
		t.Fatalf("out=%q", out.String())
	}
}

func TestUnpairRelayDownStillUnpairsLocally(t *testing.T) {
	f := &fakePairer{forgetErr: errors.New("relay request failed")}
	c, out, _ := testCLI(t, f, agentWith(&recRelay{}))
	config.Save(config.Config{Relay: "https://r.example", HelperToken: secret})
	if code := c.run(context.Background(), []string{"unpair"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	cfg, _ := config.Load()
	if !strings.Contains(out.String(), "Could not reach the relay; this computer is unpaired locally.") || cfg.HelperToken != "" {
		t.Fatalf("out=%q", out.String())
	}
}

func TestServiceConfigDirIsAbsoluteAndClean(t *testing.T) {
	if got := serviceConfigDir(""); got != "" {
		t.Fatalf("empty → %q", got)
	}
	wd, _ := os.Getwd()
	if got := serviceConfigDir("rel/dir/"); got != filepath.Join(wd, "rel", "dir") {
		t.Fatalf("relative → %q", got)
	}
	if got := serviceConfigDir("/abs/x/../y/"); got != "/abs/y" {
		t.Fatalf("absolute → %q", got)
	}
}
