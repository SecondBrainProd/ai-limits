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
	"os/user"
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
//
// On macOS the Keychain may hold more than one "Claude Code-credentials" item:
// Claude Code keeps the live one under the login user's account name, while
// other tools (for example `claude setup-token`) can leave a stale item under a
// different account. Asking the Keychain without an account returns whichever
// item comes first, which may be the dead one. So we ask for each account in
// Accounts ("" means "any account", the old behaviour) and keep the token that
// expires last.
type Source struct {
	GOOS        string
	ConfigDir   string
	Accounts    []string
	RunSecurity func(account string) ([]byte, error)
	ReadFile    func(string) ([]byte, error)
}

// keychainAccounts lists the Keychain accounts to try, most specific first.
func keychainAccounts() []string {
	var accounts []string
	name := os.Getenv("USER")
	if name == "" {
		if u, err := user.Current(); err == nil {
			name = u.Username
		}
	}
	if name != "" {
		accounts = append(accounts, name)
	}
	return append(accounts, "")
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
		Accounts:  keychainAccounts(),
		RunSecurity: func(account string) ([]byte, error) {
			args := []string{"find-generic-password", "-s", "Claude Code-credentials"}
			if account != "" {
				args = append(args, "-a", account)
			}
			return runWithTimeout("/usr/bin/security", append(args, "-w")...)
		},
		ReadFile: os.ReadFile,
	}
}

// Load reads the token. Any failure to find it yields ErrNotLoggedIn.
func (s Source) Load() (Token, error) {
	if s.GOOS == "darwin" {
		return s.loadKeychain()
	}
	if s.ReadFile == nil || s.ConfigDir == "" {
		return Token{}, ErrNotLoggedIn
	}
	raw, err := s.ReadFile(filepath.Join(s.ConfigDir, ".credentials.json"))
	if err != nil {
		return Token{}, ErrNotLoggedIn
	}
	return parse(raw)
}

// loadKeychain tries every account and keeps the token that expires last.
func (s Source) loadKeychain() (Token, error) {
	if s.RunSecurity == nil {
		return Token{}, ErrNotLoggedIn
	}
	accounts := s.Accounts
	if len(accounts) == 0 {
		accounts = []string{""}
	}
	var best Token
	found := false
	var lastErr error = ErrNotLoggedIn
	for _, account := range accounts {
		raw, err := s.RunSecurity(account)
		if errors.Is(err, ErrKeychainTimeout) {
			// A locked keychain will not answer the next query either.
			return Token{}, ErrKeychainTimeout
		}
		if err != nil {
			continue
		}
		tok, err := parse(raw)
		if err != nil {
			lastErr = err
			continue
		}
		if !found || laterExpiry(tok, best) {
			best, found = tok, true
		}
	}
	if !found {
		return Token{}, lastErr
	}
	return best, nil
}

// laterExpiry reports whether a expires after b; a zero expiry never expires.
func laterExpiry(a, b Token) bool {
	if b.ExpiresAt.IsZero() {
		return false
	}
	if a.ExpiresAt.IsZero() {
		return true
	}
	return a.ExpiresAt.After(b.ExpiresAt)
}

func parse(raw []byte) (Token, error) {
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
