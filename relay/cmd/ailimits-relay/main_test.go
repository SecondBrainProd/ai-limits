package main

import "testing"

func TestHealthURL(t *testing.T) {
	cases := map[string]string{
		":8080":        "http://127.0.0.1:8080/healthz",
		"0.0.0.0:9000": "http://127.0.0.1:9000/healthz",
		"127.0.0.1:7":  "http://127.0.0.1:7/healthz",
		"[::]:8081":    "http://127.0.0.1:8081/healthz",
	}
	for addr, want := range cases {
		got, err := healthURL(addr)
		if err != nil || got != want {
			t.Errorf("healthURL(%q) = %q, %v; want %q", addr, got, err, want)
		}
	}
	if _, err := healthURL("nonsense"); err == nil {
		t.Error("expected error for addr without port")
	}
}
