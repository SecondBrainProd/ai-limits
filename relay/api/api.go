// Package api implements the relay HTTP API used by Garmin watches and
// desktop helpers.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/SecondBrainProd/ai-limits/internal/snapshot"
	"github.com/SecondBrainProd/ai-limits/relay/store"
)

const (
	// maxBody is the maximum accepted request body size in bytes.
	maxBody = 4096
	// maxFailedPairs caps failed pairing attempts per minute across all
	// clients, to make brute-forcing codes impractical.
	maxFailedPairs = 100
	failedPairsKey = "pairfail"
)

type server struct {
	st  *store.Store
	lim *Limiter
}

// New returns the relay HTTP handler.
func New(st *store.Store, limiter *Limiter) http.Handler {
	s := &server{st: st, lim: limiter}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/watches", s.register)
	mux.HandleFunc("GET /v1/watch", s.watchState)
	mux.HandleFunc("DELETE /v1/watch", s.unpair)
	mux.HandleFunc("POST /v1/pair", s.pair)
	mux.HandleFunc("PUT /v1/snapshot", s.putSnapshot)
	mux.HandleFunc("DELETE /v1/helper", s.forgetHelper)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "ok\n")
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func unauthorized(w http.ResponseWriter) { writeErr(w, http.StatusUnauthorized, "unauthorized") }

func tooMany(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "60")
	writeErr(w, http.StatusTooManyRequests, "rate limit exceeded")
}

func internal(w http.ResponseWriter, err error) {
	log.Printf("internal error: %v", err)
	writeErr(w, http.StatusInternalServerError, "internal error")
}

// bearer returns the Authorization bearer value, or "".
func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// clientIP returns the last non-empty X-Forwarded-For entry (the hop our
// Caddy appends; earlier entries are client-controlled and spoofable), falling
// back to the RemoteAddr host. It is used only as an in-memory rate-limit key.
func clientIP(r *http.Request) string {
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		if p := strings.TrimSpace(parts[i]); p != "" {
			return p
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func isHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// readBody reads the capped body; it writes 413 and returns false when the
// body is too large, 400 on other read errors.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeErr(w, http.StatusRequestEntityTooLarge, "request body too large")
		} else {
			writeErr(w, http.StatusBadRequest, "bad request")
		}
		return nil, false
	}
	return data, true
}

// watchAuth parses "Bearer <id>.<secret>", applies the per-watch rate limit
// and returns the credentials. On failure it has already written the response.
func (s *server) watchAuth(w http.ResponseWriter, r *http.Request) (id, secret string, ok bool) {
	id, secret, found := strings.Cut(bearer(r), ".")
	// Reject malformed credentials before touching the limiter or the store,
	// so client-chosen strings never become limiter keys.
	if !found || !isHex(id, 32) || !isHex(secret, 64) {
		unauthorized(w)
		return "", "", false
	}
	if !s.lim.Allow("wip:"+clientIP(r)) || !s.lim.Allow("w:"+id) {
		tooMany(w)
		return "", "", false
	}
	return id, secret, true
}

func (s *server) register(w http.ResponseWriter, r *http.Request) {
	if !s.lim.Allow("ip:" + clientIP(r)) {
		tooMany(w)
		return
	}
	watch, err := s.st.CreateWatch(r.Context())
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":           watch.ID,
		"secret":       watch.Secret,
		"code":         watch.Code,
		"code_expires": watch.CodeExpires.Unix(),
	})
}

func (s *server) watchState(w http.ResponseWriter, r *http.Request) {
	id, secret, ok := s.watchAuth(w, r)
	if !ok {
		return
	}
	watch, snap, err := s.st.WatchState(r.Context(), id, secret)
	if errors.Is(err, store.ErrAuth) {
		unauthorized(w)
		return
	}
	if err != nil {
		internal(w, err)
		return
	}
	if watch.HelperID != "" {
		writeJSON(w, http.StatusOK, map[string]any{"paired": true, "snap": snap})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"paired":       false,
		"code":         watch.Code,
		"code_expires": watch.CodeExpires.Unix(),
	})
}

func (s *server) unpair(w http.ResponseWriter, r *http.Request) {
	id, secret, ok := s.watchAuth(w, r)
	if !ok {
		return
	}
	if err := s.st.UnpairWatch(r.Context(), id, secret); err != nil {
		if errors.Is(err, store.ErrAuth) {
			unauthorized(w)
			return
		}
		internal(w, err)
		return
	}
	watch, _, err := s.st.WatchState(r.Context(), id, secret)
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"paired":       false,
		"code":         watch.Code,
		"code_expires": watch.CodeExpires.Unix(),
	})
}

func (s *server) pair(w http.ResponseWriter, r *http.Request) {
	token := bearer(r)
	// A helper token, when present, must be well formed; reject before the
	// limiter or the store so client-chosen strings never become keys.
	if token != "" && !isHex(token, 64) {
		unauthorized(w)
		return
	}
	if !s.lim.Allow("ip:" + clientIP(r)) {
		tooMany(w)
		return
	}
	if token != "" && !s.lim.Allow(keyHash("h:", token)) {
		tooMany(w)
		return
	}
	if s.lim.Blocked(failedPairsKey, maxFailedPairs) {
		tooMany(w)
		return
	}
	data, ok := readBody(w, r)
	if !ok {
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(data, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	helper, err := s.st.Pair(r.Context(), req.Code, token)
	switch {
	case errors.Is(err, store.ErrBadCode):
		s.lim.Hit(failedPairsKey)
		writeErr(w, http.StatusNotFound, "invalid or expired code")
	case errors.Is(err, store.ErrAuth):
		unauthorized(w)
	case err != nil:
		internal(w, err)
	default:
		writeJSON(w, http.StatusOK, map[string]string{"helper_token": helper})
	}
}

func (s *server) forgetHelper(w http.ResponseWriter, r *http.Request) {
	token := bearer(r)
	if !isHex(token, 64) {
		unauthorized(w)
		return
	}
	if !s.lim.Allow("hip:"+clientIP(r)) || !s.lim.Allow(keyHash("h:", token)) {
		tooMany(w)
		return
	}
	switch err := s.st.DeleteHelper(r.Context(), token); {
	case errors.Is(err, store.ErrAuth):
		unauthorized(w)
	case err != nil:
		internal(w, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *server) putSnapshot(w http.ResponseWriter, r *http.Request) {
	token := bearer(r)
	// Helper tokens are 64 lowercase hex chars; anything else cannot be valid,
	// so reject it before it touches the limiter or the store.
	if !isHex(token, 64) {
		unauthorized(w)
		return
	}
	// Per-IP limit first, so rotating fake tokens cannot mint fresh buckets.
	if !s.lim.Allow("hip:"+clientIP(r)) || !s.lim.Allow(keyHash("h:", token)) {
		tooMany(w)
		return
	}
	// Authenticate before looking at the body so that an unauthenticated
	// caller always gets 401, never a validation hint.
	if err := s.st.AuthHelper(r.Context(), token); err != nil {
		if errors.Is(err, store.ErrAuth) {
			unauthorized(w)
			return
		}
		internal(w, err)
		return
	}
	data, ok := readBody(w, r)
	if !ok {
		return
	}
	var snap snapshot.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil || snap.Validate() != nil {
		writeErr(w, http.StatusBadRequest, "invalid snapshot")
		return
	}
	switch err := s.st.PutSnapshot(r.Context(), token, snap); {
	case errors.Is(err, store.ErrAuth):
		unauthorized(w)
	case err != nil:
		internal(w, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
