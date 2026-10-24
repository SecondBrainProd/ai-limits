package usage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/oauth/usage" {
			t.Errorf("bad request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer tok-1" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("anthropic-beta") != "oauth-2025-04-20" {
			t.Errorf("beta = %q", r.Header.Get("anthropic-beta"))
		}
		ua := r.Header.Get("User-Agent")
		if !strings.HasPrefix(ua, "ailimits/0.1 ") || !strings.Contains(ua, "https://github.com/SecondBrainProd/ai-limits") {
			t.Errorf("ua = %q", ua)
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := Client{BaseURL: srv.URL, HTTP: srv.Client(), Version: "0.1"}
	body, err := c.Fetch(context.Background(), "tok-1")
	if err != nil || string(body) != `{"ok":true}` {
		t.Fatalf("body=%q err=%v", body, err)
	}
}

func TestFetch401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer srv.Close()
	_, err := Client{BaseURL: srv.URL, HTTP: srv.Client(), Version: "0.1"}.Fetch(context.Background(), "tok-1")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
}

func TestFetch500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("echo Bearer tok-SECRET"))
	}))
	defer srv.Close()
	_, err := Client{BaseURL: srv.URL, HTTP: srv.Client(), Version: "0.1"}.Fetch(context.Background(), "tok-SECRET")
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "tok-SECRET") {
		t.Fatalf("token leaked: %v", err)
	}
}

func TestFetchTransportErrorDoesNotLeak(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()
	_, err := Client{BaseURL: url, HTTP: http.DefaultClient, Version: "0.1"}.Fetch(context.Background(), "tok-SECRET")
	if err == nil || strings.Contains(err.Error(), "tok-SECRET") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchDoesNotFollowRedirect(t *testing.T) {
	leaked := false
	mux := http.NewServeMux()
	mux.HandleFunc("/api/oauth/usage", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/other", http.StatusFound)
	})
	mux.HandleFunc("/other", func(w http.ResponseWriter, r *http.Request) {
		leaked = true
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	for _, hc := range []*http.Client{nil, srv.Client()} {
		_, err := Client{BaseURL: srv.URL, HTTP: hc, Version: "0.1"}.Fetch(context.Background(), "tok-1")
		if err == nil || !strings.Contains(err.Error(), "unexpected redirect") {
			t.Fatalf("err = %v", err)
		}
		if strings.Contains(err.Error(), "/other") {
			t.Fatalf("Location in error: %v", err)
		}
	}
	if leaked {
		t.Fatal("redirect was followed")
	}
}

func TestFetchRefusesNonHTTPS(t *testing.T) {
	called := false
	rt := roundTripFunc(func(*http.Request) (*http.Response, error) { called = true; return nil, errors.New("no") })
	for _, base := range []string{"http://example.com", "ftp://x", "://bad"} {
		_, err := Client{BaseURL: base, HTTP: &http.Client{Transport: rt}, Version: "0.1"}.Fetch(context.Background(), "tok-1")
		if err == nil || strings.Contains(err.Error(), "tok-1") {
			t.Fatalf("%s: err = %v", base, err)
		}
	}
	if called {
		t.Fatal("request was made")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetch429RateLimited(t *testing.T) {
	for hdr, want := range map[string]time.Duration{"120": 2 * time.Minute, "": 0, "soon": 0} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if hdr != "" {
				w.Header().Set("Retry-After", hdr)
			}
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		_, err := Client{BaseURL: srv.URL, Version: "t"}.Fetch(context.Background(), "tok")
		srv.Close()
		if !errors.Is(err, ErrRateLimited) {
			t.Fatalf("Retry-After %q: err = %v", hdr, err)
		}
		if got := RetryAfter(err); got != want {
			t.Fatalf("Retry-After %q: got %v want %v", hdr, got, want)
		}
	}
}
