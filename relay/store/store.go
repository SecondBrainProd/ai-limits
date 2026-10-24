// Package store is the relay's SQLite storage: watches, helpers and the
// latest usage snapshot per helper. Secrets and tokens are stored only as
// SHA-256 hashes, and no network addresses are stored at all.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/SecondBrainProd/ai-limits/internal/snapshot"
	_ "modernc.org/sqlite" // pure Go SQLite driver
)

const (
	codeAlphabet = "ACDEFGHJKMNPQRTUVWXY34679"
	codeLen      = 6
	codeTTL      = 15 * time.Minute
	watchTTL     = 24 * time.Hour
	snapshotTTL  = 30 * 24 * time.Hour
	maxCodeTries = 50
)

var (
	// ErrAuth is returned when an id/secret pair or a helper token is wrong.
	ErrAuth = errors.New("authentication failed")
	// ErrBadCode is returned when a pairing code is unknown or expired.
	ErrBadCode = errors.New("bad or expired pairing code")
)

const schema = `
CREATE TABLE IF NOT EXISTS helpers (
	id         TEXT PRIMARY KEY,
	token_hash TEXT NOT NULL UNIQUE,
	created    INTEGER NOT NULL,
	last_put   INTEGER
);
CREATE TABLE IF NOT EXISTS watches (
	id           TEXT PRIMARY KEY,
	secret_hash  TEXT NOT NULL,
	code         TEXT NOT NULL DEFAULT '',
	code_expires INTEGER NOT NULL DEFAULT 0,
	helper_id    TEXT REFERENCES helpers(id),
	created      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS watches_code ON watches(code);
CREATE TABLE IF NOT EXISTS snapshots (
	helper_id TEXT PRIMARY KEY REFERENCES helpers(id),
	body      TEXT NOT NULL,
	updated   INTEGER NOT NULL
);`

// Store is the relay storage.
type Store struct {
	db   *sql.DB
	now  func() time.Time
	rand io.Reader
}

// Watch is a watch registration as seen by the API.
type Watch struct {
	ID          string
	Secret      string // set only by CreateWatch; never stored in plain form
	Code        string // empty when paired
	CodeExpires time.Time
	HelperID    string // empty when not paired
}

// Open opens (and migrates) the database at path; ":memory:" is for tests.
func Open(path string, now func() time.Time) (*Store, error) {
	if now == nil {
		now = time.Now
	}
	dsn := path
	if path != ":memory:" {
		dsn = "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection: serializes writers and keeps ":memory:" a single database.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	// Schema version marker for future migrations: fresh databases and
	// pre-versioning databases (user_version 0) both become version 1.
	var ver int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&ver); err != nil {
		db.Close()
		return nil, fmt.Errorf("read user_version: %w", err)
	}
	if ver == 0 {
		if _, err := db.Exec(`PRAGMA user_version = 1`); err != nil {
			db.Close()
			return nil, fmt.Errorf("set user_version: %w", err)
		}
	}
	return &Store{db: db, now: now, rand: rand.Reader}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func hash(v string) string {
	h := sha256.Sum256([]byte(v))
	return hex.EncodeToString(h[:])
}

func equalHash(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (s *Store) randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(s.rand, b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// newCode draws a code from the alphabet by rejection sampling (no modulo bias).
func (s *Store) newCode() (string, error) {
	limit := byte(256 - 256%len(codeAlphabet))
	out := make([]byte, 0, codeLen)
	buf := make([]byte, 1)
	for len(out) < codeLen {
		if _, err := io.ReadFull(s.rand, buf); err != nil {
			return "", err
		}
		if buf[0] >= limit {
			continue
		}
		out = append(out, codeAlphabet[int(buf[0])%len(codeAlphabet)])
	}
	return string(out), nil
}

type execer interface {
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
}

// issueCode gives the watch a fresh code that is unique among active codes.
func (s *Store) issueCode(ctx context.Context, q execer, watchID string) (string, time.Time, error) {
	now := s.now()
	for i := 0; i < maxCodeTries; i++ {
		code, err := s.newCode()
		if err != nil {
			return "", time.Time{}, err
		}
		var n int
		if err := q.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM watches WHERE code = ? AND code_expires > ?`,
			code, now.Unix()).Scan(&n); err != nil {
			return "", time.Time{}, err
		}
		if n > 0 {
			continue
		}
		exp := now.Add(codeTTL)
		if _, err := q.ExecContext(ctx,
			`UPDATE watches SET code = ?, code_expires = ? WHERE id = ?`,
			code, exp.Unix(), watchID); err != nil {
			return "", time.Time{}, err
		}
		return code, exp, nil
	}
	return "", time.Time{}, errors.New("could not allocate a unique pairing code")
}

// CreateWatch registers a new unpaired watch with a pairing code.
func (s *Store) CreateWatch(ctx context.Context) (Watch, error) {
	id, err := s.randHex(16)
	if err != nil {
		return Watch{}, err
	}
	secret, err := s.randHex(32)
	if err != nil {
		return Watch{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Watch{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO watches(id, secret_hash, created) VALUES(?, ?, ?)`,
		id, hash(secret), s.now().Unix()); err != nil {
		return Watch{}, err
	}
	code, exp, err := s.issueCode(ctx, tx, id)
	if err != nil {
		return Watch{}, err
	}
	if err := tx.Commit(); err != nil {
		return Watch{}, err
	}
	return Watch{ID: id, Secret: secret, Code: code, CodeExpires: exp}, nil
}

// WatchState authenticates the watch and returns its state and the latest
// snapshot of its helper (nil if none yet). An unpaired watch with an expired
// code gets a new one.
func (s *Store) WatchState(ctx context.Context, id, secret string) (Watch, *snapshot.Snapshot, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Watch{}, nil, err
	}
	defer tx.Rollback()

	var secretHash, code string
	var exp int64
	var helper sql.NullString
	err = tx.QueryRowContext(ctx,
		`SELECT secret_hash, code, code_expires, helper_id FROM watches WHERE id = ?`, id).
		Scan(&secretHash, &code, &exp, &helper)
	if errors.Is(err, sql.ErrNoRows) {
		// Burn comparable time so unknown ids are not distinguishable by timing.
		equalHash(hash(secret), hash(id))
		return Watch{}, nil, ErrAuth
	}
	if err != nil {
		return Watch{}, nil, err
	}
	if !equalHash(hash(secret), secretHash) {
		return Watch{}, nil, ErrAuth
	}

	w := Watch{ID: id, HelperID: helper.String}
	var snap *snapshot.Snapshot
	if helper.Valid {
		var body string
		err := tx.QueryRowContext(ctx, `SELECT body FROM snapshots WHERE helper_id = ?`, helper.String).Scan(&body)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return Watch{}, nil, err
		default:
			var sn snapshot.Snapshot
			if err := json.Unmarshal([]byte(body), &sn); err != nil {
				return Watch{}, nil, fmt.Errorf("decode stored snapshot: %w", err)
			}
			snap = &sn
		}
	} else {
		if code == "" || exp <= s.now().Unix() {
			c, e, err := s.issueCode(ctx, tx, id)
			if err != nil {
				return Watch{}, nil, err
			}
			code, exp = c, e.Unix()
		}
		w.Code = code
		w.CodeExpires = time.Unix(exp, 0)
	}
	if err := tx.Commit(); err != nil {
		return Watch{}, nil, err
	}
	return w, snap, nil
}

// Pair links the watch that owns code to a helper. An empty helperToken
// creates a new helper and returns its token; a known token links the watch to
// that helper and returns the same token; an unknown token is ErrAuth.
func (s *Store) Pair(ctx context.Context, code, helperToken string) (string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	now := s.now().Unix()
	var watchID string
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM watches WHERE code = ? AND code_expires > ? AND helper_id IS NULL`,
		code, now).Scan(&watchID)
	if errors.Is(err, sql.ErrNoRows) || code == "" {
		return "", ErrBadCode
	}
	if err != nil {
		return "", err
	}

	var helperID string
	if helperToken == "" {
		if helperID, err = s.randHex(16); err != nil {
			return "", err
		}
		if helperToken, err = s.randHex(32); err != nil {
			return "", err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO helpers(id, token_hash, created) VALUES(?, ?, ?)`,
			helperID, hash(helperToken), now); err != nil {
			return "", err
		}
	} else {
		var tokenHash string
		err := tx.QueryRowContext(ctx,
			`SELECT id, token_hash FROM helpers WHERE token_hash = ?`, hash(helperToken)).
			Scan(&helperID, &tokenHash)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !equalHash(hash(helperToken), tokenHash)) {
			return "", ErrAuth
		}
		if err != nil {
			return "", err
		}
	}
	// The code is single-use: clear it when the watch is paired.
	if _, err := tx.ExecContext(ctx,
		`UPDATE watches SET helper_id = ?, code = '', code_expires = 0 WHERE id = ?`,
		helperID, watchID); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return helperToken, nil
}

// PutSnapshot stores the latest snapshot of the helper owning helperToken.
func (s *Store) PutSnapshot(ctx context.Context, helperToken string, snap snapshot.Snapshot) error {
	if err := snap.Validate(); err != nil {
		return fmt.Errorf("invalid snapshot: %w", err)
	}
	// The relay's clock is authoritative: watch staleness must not depend on
	// the helper's clock.
	snap.T = s.now().Unix()
	body, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	if helperToken == "" {
		return ErrAuth
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var helperID, tokenHash string
	err = tx.QueryRowContext(ctx,
		`SELECT id, token_hash FROM helpers WHERE token_hash = ?`, hash(helperToken)).
		Scan(&helperID, &tokenHash)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !equalHash(hash(helperToken), tokenHash)) {
		return ErrAuth
	}
	if err != nil {
		return err
	}
	now := s.now().Unix()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO snapshots(helper_id, body, updated) VALUES(?, ?, ?)
		 ON CONFLICT(helper_id) DO UPDATE SET body = excluded.body, updated = excluded.updated`,
		helperID, string(body), now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE helpers SET last_put = ? WHERE id = ?`, now, helperID); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteHelper forgets a helper (`ailimits unpair`): its snapshot is deleted and
// its watches become unpaired, so they show a fresh pairing code on their next
// WatchState call instead of grey data for 30 days.
func (s *Store) DeleteHelper(ctx context.Context, helperToken string) error {
	if helperToken == "" {
		return ErrAuth
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var helperID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM helpers WHERE token_hash = ?`, hash(helperToken)).Scan(&helperID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAuth
	}
	if err != nil {
		return err
	}
	now := s.now().Unix()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE watches SET helper_id = NULL, code = '', code_expires = 0, created = ? WHERE helper_id = ?`, []any{now, helperID}},
		{`DELETE FROM snapshots WHERE helper_id = ?`, []any{helperID}},
		{`DELETE FROM helpers WHERE id = ?`, []any{helperID}},
	} {
		if _, err := tx.ExecContext(ctx, q.sql, q.args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UnpairWatch detaches the watch from its helper; it gets a new pairing code
// on its next WatchState call.
func (s *Store) UnpairWatch(ctx context.Context, id, secret string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var secretHash string
	err = tx.QueryRowContext(ctx, `SELECT secret_hash FROM watches WHERE id = ?`, id).Scan(&secretHash)
	if errors.Is(err, sql.ErrNoRows) {
		equalHash(hash(secret), hash(id))
		return ErrAuth
	}
	if err != nil {
		return err
	}
	if !equalHash(hash(secret), secretHash) {
		return ErrAuth
	}
	// created restarts: the 24h cleanup clock counts from the unpairing.
	if _, err := tx.ExecContext(ctx,
		`UPDATE watches SET helper_id = NULL, code = '', code_expires = 0, created = ? WHERE id = ?`,
		s.now().Unix(), id); err != nil {
		return err
	}
	return tx.Commit()
}

// Cleanup removes unpaired watches older than 24 hours and helpers (with
// their snapshots) that have not sent a snapshot for 30 days. It returns the
// number of rows removed.
func (s *Store) Cleanup(ctx context.Context) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	now := s.now()
	watchCut := now.Add(-watchTTL).Unix()
	snapCut := now.Add(-snapshotTTL).Unix()
	total := 0
	run := func(q string, args ...any) error {
		res, err := tx.ExecContext(ctx, q, args...)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		total += int(n)
		return err
	}
	// Watches whose helper is about to go become unpaired and start a fresh clock.
	stale := `SELECT id FROM helpers WHERE COALESCE(last_put, created) < ?`
	if _, err := tx.ExecContext(ctx,
		`UPDATE watches SET helper_id = NULL, code = '', code_expires = 0, created = ?
		 WHERE helper_id IN (`+stale+`)`, now.Unix(), snapCut); err != nil {
		return 0, err
	}
	steps := []struct {
		q    string
		args []any
	}{
		{`DELETE FROM watches WHERE helper_id IS NULL AND created < ?`, []any{watchCut}},
		{`DELETE FROM snapshots WHERE updated < ? OR helper_id IN (` + stale + `)`, []any{snapCut, snapCut}},
		{`DELETE FROM helpers WHERE COALESCE(last_put, created) < ?`, []any{snapCut}},
	}
	for _, st := range steps {
		if err := run(st.q, st.args...); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return total, nil
}

// AuthHelper reports ErrAuth unless helperToken belongs to a known helper.
// It lets callers authenticate a helper before looking at a request body.
func (s *Store) AuthHelper(ctx context.Context, helperToken string) error {
	if helperToken == "" {
		return ErrAuth
	}
	var tokenHash string
	err := s.db.QueryRowContext(ctx,
		`SELECT token_hash FROM helpers WHERE token_hash = ?`, hash(helperToken)).Scan(&tokenHash)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !equalHash(hash(helperToken), tokenHash)) {
		return ErrAuth
	}
	return err
}
