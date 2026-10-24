package relayclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SecondBrainProd/ai-limits/internal/snapshot"
)

func TestPairOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || r.URL.Path != "/v1/pair" || !strings.Contains(string(b), `"code":"ABC234"`) ||
			r.Header.Get("Authorization") != "Bearer old" || r.Header.Get("User-Agent") != "ua" {
			t.Errorf("bad request %s %s %s %v", r.Method, r.URL.Path, b, r.Header)
		}
		w.Write([]byte(`{"helper_token":"new"}`))
	}))
	defer srv.Close()
	tok, err := Client{Base: srv.URL, UserAgent: "ua"}.Pair(context.Background(), "ABC234", "old")
	if err != nil || tok != "new" {
		t.Fatal(tok, err)
	}
}

func TestPairBadCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"error":"invalid or expired code"}`))
	}))
	defer srv.Close()
	if _, err := (Client{Base: srv.URL}).Pair(context.Background(), "X", ""); !errors.Is(err, ErrBadCode) {
		t.Fatal(err)
	}
}

func TestPutStatuses(t *testing.T) {
	for code, check := range map[int]func(error) bool{
		204: func(e error) bool { return e == nil },
		401: func(e error) bool { return errors.Is(e, ErrUnauthorized) },
		400: func(e error) bool { return e != nil && strings.Contains(e.Error(), "400") },
		429: func(e error) bool { return e != nil },
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "PUT" || r.URL.Path != "/v1/snapshot" || r.Header.Get("Authorization") != "Bearer h" {
				t.Errorf("bad request")
			}
			w.WriteHeader(code)
		}))
		err := Client{Base: srv.URL}.Put(context.Background(), "h", snapshot.Snapshot{V: 1})
		srv.Close()
		if !check(err) {
			t.Fatalf("code %d err %v", code, err)
		}
	}
}

func TestRefusesPlainHTTPAndRedirects(t *testing.T) {
	if err := (Client{Base: "http://example.com"}).Put(context.Background(), "SECRETTOK", snapshot.Snapshot{}); err == nil ||
		strings.Contains(err.Error(), "SECRETTOK") {
		t.Fatal(err)
	}
	hit := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, 307)
	}))
	defer srv.Close()
	if err := (Client{Base: srv.URL}).Put(context.Background(), "h", snapshot.Snapshot{}); err == nil || hit {
		t.Fatal("redirect followed", err)
	}
}

func TestNetworkErrorHasNoToken(t *testing.T) {
	err := Client{Base: "http://127.0.0.1:1"}.Put(context.Background(), "SECRETTOK", snapshot.Snapshot{})
	if err == nil || strings.Contains(err.Error(), "SECRETTOK") {
		t.Fatal(err)
	}
}

func TestPairStatuses(t *testing.T) {
	for code, check := range map[int]func(error) bool{
		401: func(e error) bool { return errors.Is(e, ErrUnauthorized) },
		404: func(e error) bool { return errors.Is(e, ErrBadCode) },
		429: func(e error) bool { return errors.Is(e, ErrRateLimited) },
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
		_, err := Client{Base: srv.URL}.Pair(context.Background(), "ABC234", "h")
		srv.Close()
		if !check(err) {
			t.Fatalf("code %d err %v", code, err)
		}
	}
}

func TestUnauthorizedMessage(t *testing.T) {
	want := "This computer is no longer paired. Open AI Limits on your watch and run: ailimits pair <CODE>"
	if ErrUnauthorized.Error() != want {
		t.Fatal(ErrUnauthorized)
	}
}

func TestForget(t *testing.T) {
	for code, want := range map[int]error{204: nil, 401: ErrUnauthorized} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "DELETE" || r.URL.Path != "/v1/helper" || r.Header.Get("Authorization") != "Bearer tok" {
				t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			}
			w.WriteHeader(code)
		}))
		err := Client{Base: srv.URL}.Forget(context.Background(), "tok")
		srv.Close()
		if !errors.Is(err, want) && !(want == nil && err == nil) {
			t.Fatalf("code %d: err = %v", code, err)
		}
	}
}
