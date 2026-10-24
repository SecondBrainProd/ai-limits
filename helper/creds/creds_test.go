package creds

import (
	"errors"
	"testing"
	"time"
)

const sample = `{"claudeAiOauth":{"accessToken":"sk-test-token","expiresAt":1800000000000}}`

func TestLoadDarwinFromSecurity(t *testing.T) {
	s := Source{GOOS: "darwin", RunSecurity: func() ([]byte, error) { return []byte(sample + "\n"), nil }}
	tok, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if tok.Access != "sk-test-token" {
		t.Fatalf("access = %q", tok.Access)
	}
	if !tok.ExpiresAt.Equal(time.UnixMilli(1800000000000)) {
		t.Fatalf("expires = %v", tok.ExpiresAt)
	}
}

func TestLoadLinuxFile(t *testing.T) {
	var got string
	s := Source{GOOS: "linux", ConfigDir: "/cfg", ReadFile: func(p string) ([]byte, error) {
		got = p
		return []byte(sample), nil
	}}
	tok, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/cfg/.credentials.json" || tok.Access != "sk-test-token" {
		t.Fatalf("path=%q tok=%+v", got, tok)
	}
}

func TestLoadMissing(t *testing.T) {
	s := Source{GOOS: "linux", ConfigDir: "/cfg", ReadFile: func(string) ([]byte, error) { return nil, errors.New("nope") }}
	if _, err := s.Load(); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("err = %v", err)
	}
	d := Source{GOOS: "darwin", RunSecurity: func() ([]byte, error) { return nil, errors.New("exit 44") }}
	if _, err := d.Load(); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("darwin err = %v", err)
	}
}

func TestLoadEmptyAccess(t *testing.T) {
	s := Source{GOOS: "linux", ConfigDir: "/cfg", ReadFile: func(string) ([]byte, error) {
		return []byte(`{"claudeAiOauth":{"accessToken":"","expiresAt":0}}`), nil
	}}
	if _, err := s.Load(); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadMalformedDoesNotLeak(t *testing.T) {
	s := Source{GOOS: "linux", ConfigDir: "/cfg", ReadFile: func(string) ([]byte, error) {
		return []byte(`{"claudeAiOauth":{"accessToken":"SECRET-xyz",`), nil
	}}
	_, err := s.Load()
	if err == nil {
		t.Fatal("want error")
	}
	if containsStr(err.Error(), "SECRET") {
		t.Fatalf("token leaked: %v", err)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestExpired(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	if (Token{Access: "a"}).Expired(now) {
		t.Fatal("zero expiry must be non-expiring")
	}
	if !(Token{Access: "a", ExpiresAt: now.Add(-time.Second)}).Expired(now) {
		t.Fatal("past must be expired")
	}
	if (Token{Access: "a", ExpiresAt: now.Add(time.Minute)}).Expired(now) {
		t.Fatal("future must not be expired")
	}
}

func TestRunWithTimeoutDoesNotBlock(t *testing.T) {
	old := keychainTimeout
	keychainTimeout = 200 * time.Millisecond
	defer func() { keychainTimeout = old }()
	start := time.Now()
	_, err := runWithTimeout("/bin/sleep", "5")
	if !errors.Is(err, ErrKeychainTimeout) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("blocked for %v", time.Since(start))
	}
}

func TestLoadPropagatesKeychainTimeout(t *testing.T) {
	s := Source{GOOS: "darwin", RunSecurity: func() ([]byte, error) { return nil, ErrKeychainTimeout }}
	if _, err := s.Load(); !errors.Is(err, ErrKeychainTimeout) {
		t.Fatalf("err = %v", err)
	}
}
