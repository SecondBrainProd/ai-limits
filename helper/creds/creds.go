// Package creds reads the local Claude Code subscription token. It is strictly
// read-only: it never refreshes the token and never writes to Claude Code's
// storage. Errors never contain token material.
package creds

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// ErrNotLoggedIn means no usable token was found.
var ErrNotLoggedIn = errors.New("Claude Code is not logged in (no subscription token found)")

// ErrKeychainTimeout means the macOS Keychain read did not finish in time.
var ErrKeychainTimeout = errors.New("timed out reading the Claude Code token from the macOS Keychain (is the keychain locked?)")

// keychainTimeout bounds the keychain read; a package var so tests can shorten it.
var keychainTimeout = 10 * time.Second

// runWithTimeout runs a command and returns its stdout, giving up after keychainTimeout.
func runWithTimeout(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), keychainTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, ErrKeychainTimeout
	}
	return out, err
}

// Token is the OAuth access token and its expiry. A zero ExpiresAt means no expiry.
type Token struct {
	Access    string
	ExpiresAt time.Time
}

// Source describes where to look for the token; fields are injectable for tests.
type Source struct {
	GOOS        string
	ConfigDir   string
	RunSecurity func() ([]byte, error)
	ReadFile    func(string) ([]byte, error)
}

// Default returns a Source for the current machine.
func Default() Source {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, ".claude")
		}
	}
	return Source{
		GOOS:      runtime.GOOS,
		ConfigDir: dir,
		RunSecurity: func() ([]byte, error) {
			return runWithTimeout("/usr/bin/security", "find-generic-password", "-s", "Claude Code-credentials", "-w")
		},
		ReadFile: os.ReadFile,
	}
}

// Load reads the token. Any failure to find it yields ErrNotLoggedIn.
func (s Source) Load() (Token, error) {
	var raw []byte
	var err error
	if s.GOOS == "darwin" {
		if s.RunSecurity == nil {
			return Token{}, ErrNotLoggedIn
		}
		raw, err = s.RunSecurity()
	} else {
		if s.ReadFile == nil || s.ConfigDir == "" {
			return Token{}, ErrNotLoggedIn
		}
		raw, err = s.ReadFile(filepath.Join(s.ConfigDir, ".credentials.json"))
	}
	if errors.Is(err, ErrKeychainTimeout) {
		return Token{}, ErrKeychainTimeout
	}
	if err != nil {
		return Token{}, ErrNotLoggedIn
	}
	var doc struct {
		OAuth struct {
			AccessToken string `json:"accessToken"`
			ExpiresAt   int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		// Deliberately drop err: a decode error may quote input bytes.
		return Token{}, errors.New("Claude Code credentials are not valid JSON")
	}
	if doc.OAuth.AccessToken == "" {
		return Token{}, ErrNotLoggedIn
	}
	t := Token{Access: doc.OAuth.AccessToken}
	if doc.OAuth.ExpiresAt > 0 {
		t.ExpiresAt = time.UnixMilli(doc.OAuth.ExpiresAt)
	}
	return t, nil
}

// Expired reports whether the token has expired at now.
func (t Token) Expired(now time.Time) bool {
	return !t.ExpiresAt.IsZero() && !now.Before(t.ExpiresAt)
}
