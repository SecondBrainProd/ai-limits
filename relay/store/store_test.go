package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SecondBrainProd/ai-limits/internal/snapshot"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newStore(t *testing.T) (*Store, *clock) {
	t.Helper()
	c := &clock{t: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)}
	s, err := Open(":memory:", c.now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, c
}

func snap(pct int) snapshot.Snapshot {
	return snapshot.Snapshot{V: snapshot.Version, T: 1000, Rows: []snapshot.Row{{Kind: "session", Pct: pct, Reset: 2000}}}
}

func pairNew(t *testing.T, s *Store) (Watch, string) {
	t.Helper()
	ctx := context.Background()
	w, err := s.CreateWatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := s.Pair(ctx, w.Code, "")
	if err != nil {
		t.Fatal(err)
	}
	return w, tok
}

func TestCreateWatchUnique(t *testing.T) {
	s, c := newStore(t)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		w, err := s.CreateWatch(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(w.Code) != 6 || len(w.ID) != 32 || len(w.Secret) != 64 {
			t.Fatalf("bad lengths: %+v", w)
		}
		for _, r := range w.Code {
			if !strings.ContainsRune(codeAlphabet, r) {
				t.Fatalf("code %q has char outside alphabet", w.Code)
			}
		}
		if seen[w.Code] {
			t.Fatalf("duplicate code %q", w.Code)
		}
		seen[w.Code] = true
		if !w.CodeExpires.Equal(c.now().Add(15 * time.Minute)) {
			t.Fatalf("expiry = %v", w.CodeExpires)
		}
	}
}

func TestWatchStateExpiredCodeRenews(t *testing.T) {
	s, c := newStore(t)
	ctx := context.Background()
	w, _ := s.CreateWatch(ctx)
	c.advance(16 * time.Minute)
	got, snap, err := s.WatchState(ctx, w.ID, w.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if snap != nil {
		t.Fatal("expected no snapshot")
	}
	if got.Code == "" || !got.CodeExpires.Equal(c.now().Add(15*time.Minute)) {
		t.Fatalf("code not renewed: %+v", got)
	}
	if _, err := s.Pair(ctx, got.Code, ""); err != nil {
		t.Fatalf("renewed code should pair: %v", err)
	}
}

func TestWatchStateKeepsValidCode(t *testing.T) {
	s, c := newStore(t)
	ctx := context.Background()
	w, _ := s.CreateWatch(ctx)
	c.advance(5 * time.Minute)
	got, _, _ := s.WatchState(ctx, w.ID, w.Secret)
	if got.Code != w.Code || !got.CodeExpires.Equal(w.CodeExpires) {
		t.Fatalf("code changed: %+v vs %+v", got, w)
	}
}

func TestPairNewHelperThenSnapshot(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	w, tok := pairNew(t, s)
	if len(tok) != 64 {
		t.Fatalf("token length %d", len(tok))
	}
	st, sn, err := s.WatchState(ctx, w.ID, w.Secret)
	if err != nil || sn != nil || st.HelperID == "" || st.Code != "" {
		t.Fatalf("after pair: %+v %v %v", st, sn, err)
	}
	if err := s.PutSnapshot(ctx, tok, snap(42)); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSnapshot(ctx, tok, snap(43)); err != nil { // replaces
		t.Fatal(err)
	}
	_, sn, err = s.WatchState(ctx, w.ID, w.Secret)
	if err != nil || sn == nil || sn.Rows[0].Pct != 43 || sn.Rows[0].Reset != 2000 {
		t.Fatalf("snapshot = %+v, %v", sn, err)
	}
}

func TestPairSameHelperTwoWatches(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	w1, tok := pairNew(t, s)
	w2, _ := s.CreateWatch(ctx)
	tok2, err := s.Pair(ctx, w2.Code, tok)
	if err != nil || tok2 != tok {
		t.Fatalf("token = %q err = %v", tok2, err)
	}
	if err := s.PutSnapshot(ctx, tok, snap(7)); err != nil {
		t.Fatal(err)
	}
	for _, w := range []Watch{w1, w2} {
		_, sn, err := s.WatchState(ctx, w.ID, w.Secret)
		if err != nil || sn == nil || sn.Rows[0].Pct != 7 {
			t.Fatalf("watch %s: %+v %v", w.ID, sn, err)
		}
	}
}

func TestPairUnknownHelperToken(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	w, _ := s.CreateWatch(ctx)
	if _, err := s.Pair(ctx, w.Code, "deadbeef"); !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v", err)
	}
	// The failed attempt must not burn the code.
	if _, err := s.Pair(ctx, w.Code, ""); err != nil {
		t.Fatalf("code should still work: %v", err)
	}
}

func TestRepairReplacesHelper(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	w, tokA := pairNew(t, s)
	s.PutSnapshot(ctx, tokA, snap(10))
	_, tokB := pairNew(t, s)
	s.PutSnapshot(ctx, tokB, snap(90))

	if err := s.UnpairWatch(ctx, w.ID, w.Secret); err != nil {
		t.Fatal(err)
	}
	st, _, err := s.WatchState(ctx, w.ID, w.Secret)
	if err != nil || st.Code == "" {
		t.Fatalf("no new code after unpair: %+v %v", st, err)
	}
	if _, err := s.Pair(ctx, st.Code, tokB); err != nil {
		t.Fatal(err)
	}
	_, sn, _ := s.WatchState(ctx, w.ID, w.Secret)
	if sn == nil || sn.Rows[0].Pct != 90 {
		t.Fatalf("snapshot = %+v", sn)
	}
}

func TestPairBadOrExpiredCode(t *testing.T) {
	s, c := newStore(t)
	ctx := context.Background()
	if _, err := s.Pair(ctx, "ZZZZZZ", ""); !errors.Is(err, ErrBadCode) {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := s.Pair(ctx, "", ""); !errors.Is(err, ErrBadCode) {
		t.Fatalf("empty: %v", err)
	}
	w, _ := s.CreateWatch(ctx)
	c.advance(16 * time.Minute)
	if _, err := s.Pair(ctx, w.Code, ""); !errors.Is(err, ErrBadCode) {
		t.Fatalf("expired: %v", err)
	}
	w2, _ := s.CreateWatch(ctx)
	if _, err := s.Pair(ctx, w2.Code, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pair(ctx, w2.Code, ""); !errors.Is(err, ErrBadCode) {
		t.Fatalf("reused: %v", err)
	}
}

func TestWrongSecret(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	w, _ := s.CreateWatch(ctx)
	if _, _, err := s.WatchState(ctx, w.ID, "wrong"); !errors.Is(err, ErrAuth) {
		t.Fatalf("wrong secret: %v", err)
	}
	if _, _, err := s.WatchState(ctx, "nope", w.Secret); !errors.Is(err, ErrAuth) {
		t.Fatalf("wrong id: %v", err)
	}
	if err := s.UnpairWatch(ctx, w.ID, "wrong"); !errors.Is(err, ErrAuth) {
		t.Fatalf("unpair wrong secret: %v", err)
	}
	if err := s.PutSnapshot(ctx, "wrong", snap(1)); !errors.Is(err, ErrAuth) {
		t.Fatalf("put wrong token: %v", err)
	}
	if err := s.PutSnapshot(ctx, "", snap(1)); !errors.Is(err, ErrAuth) {
		t.Fatalf("put empty token: %v", err)
	}
}

func TestPutInvalidSnapshot(t *testing.T) {
	s, _ := newStore(t)
	_, tok := pairNew(t, s)
	bad := snapshot.Snapshot{V: snapshot.Version, T: 1, Rows: make([]snapshot.Row, 4)}
	for i := range bad.Rows {
		bad.Rows[i] = snapshot.Row{Kind: "session"}
	}
	if err := s.PutSnapshot(context.Background(), tok, bad); err == nil {
		t.Fatal("expected error for 4 rows")
	}
}

func TestUnpair(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	w, tok := pairNew(t, s)
	s.PutSnapshot(ctx, tok, snap(5))
	if err := s.UnpairWatch(ctx, w.ID, w.Secret); err != nil {
		t.Fatal(err)
	}
	st, sn, err := s.WatchState(ctx, w.ID, w.Secret)
	if err != nil || sn != nil || st.HelperID != "" || st.Code == "" {
		t.Fatalf("after unpair: %+v %v %v", st, sn, err)
	}
}

func TestCleanup(t *testing.T) {
	s, c := newStore(t)
	ctx := context.Background()
	old, _ := s.CreateWatch(ctx) // unpaired, will age out
	paired, tok := pairNew(t, s) // paired, helper keeps sending
	idle, _ := pairNew(t, s)     // paired, helper never sends
	s.PutSnapshot(ctx, tok, snap(1))
	if n, err := s.Cleanup(ctx); err != nil || n != 0 {
		t.Fatalf("nothing should go yet: %d %v", n, err)
	}

	c.advance(25 * time.Hour)
	n, err := s.Cleanup(ctx)
	if err != nil || n != 1 {
		t.Fatalf("after 25h: removed %d, err %v", n, err)
	}
	if _, _, err := s.WatchState(ctx, old.ID, old.Secret); !errors.Is(err, ErrAuth) {
		t.Fatalf("old watch should be gone: %v", err)
	}

	c.advance(29 * 24 * time.Hour)   // 30d+1h since start
	s.PutSnapshot(ctx, tok, snap(2)) // active helper stays fresh
	n, err = s.Cleanup(ctx)
	// idle helper removed (helper row only; it never had a snapshot)
	if err != nil || n != 1 {
		t.Fatalf("after 30d: removed %d, err %v", n, err)
	}
	st, sn, err := s.WatchState(ctx, idle.ID, idle.Secret)
	if err != nil || st.HelperID != "" || st.Code == "" || sn != nil {
		t.Fatalf("idle watch should be unpaired with a code: %+v %v %v", st, sn, err)
	}
	if _, sn, _ := s.WatchState(ctx, paired.ID, paired.Secret); sn == nil {
		t.Fatal("active helper snapshot must survive")
	}

	// A silent helper's snapshot goes with it.
	c.advance(31 * 24 * time.Hour)
	n, err = s.Cleanup(ctx)
	// silent helper + its snapshot + the idle watch (unpaired >24h ago).
	if err != nil || n != 3 {
		t.Fatalf("after 61d: removed %d, err %v", n, err)
	}
	if _, sn, _ := s.WatchState(ctx, paired.ID, paired.Secret); sn != nil {
		t.Fatal("stale snapshot must be removed")
	}
}

func TestSecretsNotStoredPlain(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	w, tok := pairNew(t, s)
	s.PutSnapshot(ctx, tok, snap(1))
	for _, table := range []string{"watches", "helpers", "snapshots"} {
		rows, err := s.db.Query("SELECT * FROM " + table)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			for _, v := range vals {
				str := ""
				switch x := v.(type) {
				case string:
					str = x
				case []byte:
					str = string(x)
				}
				if str != "" && (strings.Contains(str, w.Secret) || strings.Contains(str, tok)) {
					t.Fatalf("plain secret or token found in %s", table)
				}
			}
		}
		rows.Close()
	}
}

func TestAuthHelper(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if err := s.AuthHelper(ctx, ""); !errors.Is(err, ErrAuth) {
		t.Fatalf("empty: %v", err)
	}
	if err := s.AuthHelper(ctx, "nope"); !errors.Is(err, ErrAuth) {
		t.Fatalf("unknown: %v", err)
	}
	w, err := s.CreateWatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := s.Pair(ctx, w.Code, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AuthHelper(ctx, tok); err != nil {
		t.Fatalf("valid: %v", err)
	}
}

func TestPutSnapshotOverwritesT(t *testing.T) {
	s, c := newStore(t)
	ctx := context.Background()
	w, tok := pairNew(t, s)
	in := snap(10)
	in.T = 5 // helper clock is wrong
	if err := s.PutSnapshot(ctx, tok, in); err != nil {
		t.Fatal(err)
	}
	_, sn, err := s.WatchState(ctx, w.ID, w.Secret)
	if err != nil || sn == nil {
		t.Fatalf("state: %v %v", sn, err)
	}
	if sn.T != c.now().Unix() {
		t.Fatalf("T = %d, want relay time %d", sn.T, c.now().Unix())
	}
}

func userVersion(t *testing.T, s *Store) int {
	t.Helper()
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestOpenSetsUserVersion(t *testing.T) {
	s, _ := newStore(t)
	if v := userVersion(t, s); v != 1 {
		t.Fatalf("fresh user_version = %d", v)
	}
}

func TestOpenUpgradesLegacyDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	s, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a database created before versioning.
	if _, err := s.db.Exec(`PRAGMA user_version = 0`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v := userVersion(t, s); v != 1 {
		t.Fatalf("legacy user_version = %d", v)
	}
}

func TestDeleteHelperUnpairsWatches(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	w, tok := pairNew(t, s)
	if err := s.PutSnapshot(ctx, tok, snap(42)); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteHelper(ctx, tok); err != nil {
		t.Fatal(err)
	}
	st, sn, err := s.WatchState(ctx, w.ID, w.Secret)
	if err != nil || sn != nil || st.HelperID != "" || st.Code == "" {
		t.Fatalf("after forget: %+v %v %v", st, sn, err)
	}
	if err := s.PutSnapshot(ctx, tok, snap(43)); !errors.Is(err, ErrAuth) {
		t.Fatalf("token still valid after forget: %v", err)
	}
}

func TestDeleteHelperUnknown401(t *testing.T) {
	s, _ := newStore(t)
	if err := s.DeleteHelper(context.Background(), strings.Repeat("a", 64)); !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v", err)
	}
}
