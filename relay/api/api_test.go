package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SecondBrainProd/ai-limits/internal/snapshot"
	"github.com/SecondBrainProd/ai-limits/relay/store"
)

type env struct {
	t   *testing.T
	h   http.Handler
	now time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, now: time.Unix(1_800_000_000, 0)}
	clock := func() time.Time { return e.now }
	st, err := store.Open(":memory:", clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e.h = New(st, NewLimiter(clock))
	return e
}

func (e *env) do(method, path, bearer, body string, hdr ...string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
}

type watchResp struct {
	ID          string             `json:"id"`
	Secret      string             `json:"secret"`
	Code        string             `json:"code"`
	CodeExpires int64              `json:"code_expires"`
	Paired      bool               `json:"paired"`
	Snap        *snapshot.Snapshot `json:"snap"`
}

func (e *env) register() (watchResp, string) {
	e.t.Helper()
	rec := e.do("POST", "/v1/watches", "", "")
	if rec.Code != 201 {
		e.t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}
	var w watchResp
	decode(e.t, rec, &w)
	if w.ID == "" || w.Secret == "" || w.Code == "" || w.CodeExpires == 0 {
		e.t.Fatalf("incomplete register response: %+v", w)
	}
	return w, w.ID + "." + w.Secret
}

const goodSnap = `{"v":1,"t":1800000000,"rows":[{"kind":"session","pct":42,"reset":1800003600,"sev":0}]}`

func TestFullScenario(t *testing.T) {
	e := newEnv(t)
	w, auth := e.register()

	rec := e.do("GET", "/v1/watch", auth, "")
	var st watchResp
	decode(t, rec, &st)
	if rec.Code != 200 || st.Paired || st.Code != w.Code {
		t.Fatalf("unpaired state: %d %s", rec.Code, rec.Body)
	}

	rec = e.do("POST", "/v1/pair", "", `{"code":"`+w.Code+`"}`)
	var pr struct {
		HelperToken string `json:"helper_token"`
	}
	decode(t, rec, &pr)
	if rec.Code != 200 || pr.HelperToken == "" {
		t.Fatalf("pair: %d %s", rec.Code, rec.Body)
	}

	if rec = e.do("PUT", "/v1/snapshot", pr.HelperToken, goodSnap); rec.Code != 204 {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}

	rec = e.do("GET", "/v1/watch", auth, "")
	st = watchResp{}
	decode(t, rec, &st)
	if rec.Code != 200 || !st.Paired || st.Snap == nil || st.Snap.Rows[0].Pct != 42 || st.Snap.T != 1800000000 {
		t.Fatalf("paired state: %d %s", rec.Code, rec.Body)
	}
}

func TestPairedWithoutSnapshotIsNull(t *testing.T) {
	e := newEnv(t)
	w, auth := e.register()
	e.do("POST", "/v1/pair", "", `{"code":"`+w.Code+`"}`)
	rec := e.do("GET", "/v1/watch", auth, "")
	if !strings.Contains(rec.Body.String(), `"snap":null`) || !strings.Contains(rec.Body.String(), `"paired":true`) {
		t.Fatalf("body: %s", rec.Body)
	}
}

func TestWatchGetRenewsCode(t *testing.T) {
	e := newEnv(t)
	w, auth := e.register()
	e.now = e.now.Add(16 * time.Minute)
	rec := e.do("GET", "/v1/watch", auth, "")
	var st watchResp
	decode(t, rec, &st)
	if rec.Code != 200 || st.Paired || st.Code == "" || st.CodeExpires <= e.now.Unix() {
		t.Fatalf("expected fresh code: %d %s", rec.Code, rec.Body)
	}
	_ = w
}

func TestWatchUnauthorized401(t *testing.T) {
	e := newEnv(t)
	w, _ := e.register()
	for _, bearer := range []string{"", "garbage", w.ID + ".wrong", "nope." + w.Secret} {
		rec := e.do("GET", "/v1/watch", bearer, "")
		if rec.Code != 401 || strings.TrimSpace(rec.Body.String()) != `{"error":"unauthorized"}` {
			t.Errorf("bearer %q: %d %s", bearer, rec.Code, rec.Body)
		}
	}
}

func TestPairBadCode404(t *testing.T) {
	e := newEnv(t)
	rec := e.do("POST", "/v1/pair", "", `{"code":"AAAAAA"}`)
	if rec.Code != 404 || strings.TrimSpace(rec.Body.String()) != `{"error":"invalid or expired code"}` {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestPairBadBody400(t *testing.T) {
	e := newEnv(t)
	if rec := e.do("POST", "/v1/pair", "", `not json`); rec.Code != 400 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestPairWithExistingHelperToken(t *testing.T) {
	e := newEnv(t)
	w1, _ := e.register()
	w2, _ := e.register()
	var p1, p2 struct {
		HelperToken string `json:"helper_token"`
	}
	decode(t, e.do("POST", "/v1/pair", "", `{"code":"`+w1.Code+`"}`), &p1)
	decode(t, e.do("POST", "/v1/pair", p1.HelperToken, `{"code":"`+w2.Code+`"}`), &p2)
	if p2.HelperToken != p1.HelperToken {
		t.Fatalf("token changed: %q vs %q", p1.HelperToken, p2.HelperToken)
	}
	w3, _ := e.register()
	if rec := e.do("POST", "/v1/pair", "unknowntoken", `{"code":"`+w3.Code+`"}`); rec.Code != 401 {
		t.Fatalf("unknown token: %d", rec.Code)
	}
}

func TestPutUnauthorized401(t *testing.T) {
	e := newEnv(t)
	if rec := e.do("PUT", "/v1/snapshot", "", goodSnap); rec.Code != 401 {
		t.Fatalf("no token: %d", rec.Code)
	}
	if rec := e.do("PUT", "/v1/snapshot", "bogus", goodSnap); rec.Code != 401 {
		t.Fatalf("bad token: %d", rec.Code)
	}
	// An unauthenticated caller gets 401 even with an invalid body.
	if rec := e.do("PUT", "/v1/snapshot", "bogus", `{"v":99}`); rec.Code != 401 {
		t.Fatalf("bad token + bad body: %d", rec.Code)
	}
	if rec := e.do("PUT", "/v1/snapshot", "bogus", `not json`); rec.Code != 401 {
		t.Fatalf("bad token + junk body: %d", rec.Code)
	}
}

func pairedHelper(e *env) string {
	w, _ := e.register()
	var p struct {
		HelperToken string `json:"helper_token"`
	}
	decode(e.t, e.do("POST", "/v1/pair", "", `{"code":"`+w.Code+`"}`), &p)
	return p.HelperToken
}

func TestPutInvalid400(t *testing.T) {
	e := newEnv(t)
	tok := pairedHelper(e)
	for _, body := range []string{`{"v":99,"t":1,"rows":[]}`, `not json`, `{"v":1,"t":1,"rows":[{"kind":"session","pct":500}]}`} {
		if rec := e.do("PUT", "/v1/snapshot", tok, body); rec.Code != 400 {
			t.Errorf("body %q: %d", body, rec.Code)
		}
	}
}

func TestBodyTooLarge413(t *testing.T) {
	e := newEnv(t)
	tok := pairedHelper(e)
	big := `{"v":1,"t":1,"rows":[],"pad":"` + strings.Repeat("x", 5000) + `"}`
	if rec := e.do("PUT", "/v1/snapshot", tok, big); rec.Code != 413 {
		t.Fatalf("put: %d", rec.Code)
	}
	if rec := e.do("POST", "/v1/pair", "", `{"code":"`+strings.Repeat("A", 5000)+`"}`); rec.Code != 413 {
		t.Fatalf("pair: %d", rec.Code)
	}
}

func TestRateLimit429(t *testing.T) {
	e := newEnv(t)
	_, auth := e.register()
	for i := 1; i <= 30; i++ {
		if rec := e.do("GET", "/v1/watch", auth, ""); rec.Code != 200 {
			t.Fatalf("request %d: %d", i, rec.Code)
		}
	}
	if rec := e.do("GET", "/v1/watch", auth, ""); rec.Code != 429 {
		t.Fatalf("31st request: %d", rec.Code)
	}
	e.now = e.now.Add(61 * time.Second)
	if rec := e.do("GET", "/v1/watch", auth, ""); rec.Code != 200 {
		t.Fatalf("after window: %d", rec.Code)
	}
}

func TestRateLimitHelperToken(t *testing.T) {
	e := newEnv(t)
	tok := pairedHelper(e)
	var last int
	for i := 0; i < 31; i++ {
		last = e.do("PUT", "/v1/snapshot", tok, goodSnap).Code
	}
	if last != 429 {
		t.Fatalf("31st put: %d", last)
	}
}

func TestRateLimitRegisterByForwardedIP(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 30; i++ {
		if rec := e.do("POST", "/v1/watches", "", "", "X-Forwarded-For", "10.0.0.1, 203.0.113.7"); rec.Code != 201 {
			t.Fatalf("request %d: %d", i, rec.Code)
		}
	}
	if rec := e.do("POST", "/v1/watches", "", "", "X-Forwarded-For", "203.0.113.7"); rec.Code != 429 {
		t.Fatalf("31st same IP: %d", rec.Code)
	}
	if rec := e.do("POST", "/v1/watches", "", "", "X-Forwarded-For", "203.0.113.8"); rec.Code != 201 {
		t.Fatalf("other IP: %d", rec.Code)
	}
}

func TestRateLimitPairByIP(t *testing.T) {
	e := newEnv(t)
	var last int
	for i := 0; i < 31; i++ {
		last = e.do("POST", "/v1/pair", "", `{"code":"AAAAAA"}`, "X-Forwarded-For", "198.51.100.1").Code
	}
	if last != 429 {
		t.Fatalf("31st pair: %d", last)
	}
}

func TestDeleteWatchUnpairs(t *testing.T) {
	e := newEnv(t)
	w, auth := e.register()
	e.do("POST", "/v1/pair", "", `{"code":"`+w.Code+`"}`)
	rec := e.do("DELETE", "/v1/watch", auth, "")
	var st watchResp
	decode(t, rec, &st)
	if rec.Code != 200 || st.Paired || st.Code == "" || st.CodeExpires == 0 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	rec = e.do("GET", "/v1/watch", auth, "")
	st = watchResp{}
	decode(t, rec, &st)
	if st.Paired {
		t.Fatalf("still paired: %s", rec.Body)
	}
	if rec := e.do("DELETE", "/v1/watch", "bad.bad", ""); rec.Code != 401 {
		t.Fatalf("unauth delete: %d", rec.Code)
	}
}

func TestHealthz(t *testing.T) {
	e := newEnv(t)
	rec := e.do("GET", "/healthz", "", "")
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != "ok" {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	e := newEnv(t)
	if rec := e.do("POST", "/v1/watch", "", ""); rec.Code != 405 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestMalformedWatchCredentials401BeforeLimiter(t *testing.T) {
	e := newEnv(t)
	huge := strings.Repeat("a", 1<<20)
	good := strings.Repeat("a", 32) + "." + strings.Repeat("b", 64)
	bad := []string{
		huge + "." + huge,
		strings.Repeat("a", 31) + "." + strings.Repeat("b", 64),
		strings.Repeat("A", 32) + "." + strings.Repeat("b", 64),
		strings.Repeat("a", 32) + "." + strings.Repeat("b", 63),
		strings.Repeat("a", 32) + "." + strings.Repeat("g", 64),
	}
	for i := 0; i < 40; i++ {
		for _, b := range bad {
			if rec := e.do("GET", "/v1/watch", b, ""); rec.Code != 401 {
				t.Fatalf("malformed %.20q: %d", b, rec.Code)
			}
		}
	}
	// Malformed attempts must not have consumed the IP budget.
	if rec := e.do("GET", "/v1/watch", good, ""); rec.Code != 401 {
		t.Fatalf("well-formed unknown id: %d", rec.Code)
	}
}

func TestWatchRateLimitByIPRotatingIDs(t *testing.T) {
	e := newEnv(t)
	var last int
	for i := 0; i < 31; i++ {
		id := strings.Repeat("0", 30) + string("0123456789abcdef"[i/16]) + string("0123456789abcdef"[i%16])
		last = e.do("GET", "/v1/watch", id+"."+strings.Repeat("c", 64), "", "X-Forwarded-For", "203.0.113.50").Code
	}
	if last != 429 {
		t.Fatalf("31st rotating-id request: %d", last)
	}
	if rec := e.do("DELETE", "/v1/watch", strings.Repeat("1", 32)+"."+strings.Repeat("c", 64), "", "X-Forwarded-For", "203.0.113.50"); rec.Code != 429 {
		t.Fatalf("delete from limited IP: %d", rec.Code)
	}
}

func TestClientIPUsesLastForwardedEntry(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 30; i++ {
		// Client rotates the spoofable leading entries; Caddy appends 1.2.3.4.
		xff := "6.6.6." + string(rune('a'+i)) + ", 1.2.3.4"
		if rec := e.do("POST", "/v1/watches", "", "", "X-Forwarded-For", xff); rec.Code != 201 {
			t.Fatalf("request %d: %d", i, rec.Code)
		}
	}
	if rec := e.do("POST", "/v1/watches", "", "", "X-Forwarded-For", "9.9.9.9, 1.2.3.4"); rec.Code != 429 {
		t.Fatalf("31st via same last hop: %d", rec.Code)
	}
}

func TestGlobalFailedPairCap(t *testing.T) {
	e := newEnv(t)
	w, _ := e.register()
	for i := 0; i < 100; i++ {
		ip := "10.0." + string(rune('0'+i/10)) + "." + string(rune('0'+i%10))
		if rec := e.do("POST", "/v1/pair", "", `{"code":"AAAAAA"}`, "X-Forwarded-For", ip); rec.Code != 404 {
			t.Fatalf("attempt %d: %d", i, rec.Code)
		}
	}
	// Cap reached: even a correct code from a fresh client is refused.
	if rec := e.do("POST", "/v1/pair", "", `{"code":"`+w.Code+`"}`, "X-Forwarded-For", "10.9.9.9"); rec.Code != 429 {
		t.Fatalf("after cap: %d", rec.Code)
	}
	e.now = e.now.Add(61 * time.Second)
	if rec := e.do("POST", "/v1/pair", "", `{"code":"`+w.Code+`"}`, "X-Forwarded-For", "10.9.9.9"); rec.Code != 200 {
		t.Fatalf("after window: %d", rec.Code)
	}
}

func TestSuccessfulPairsDoNotCountTowardFailCap(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 5; i++ {
		w, _ := e.register()
		ip := "10.1.0." + string(rune('0'+i))
		if rec := e.do("POST", "/v1/pair", "", `{"code":"`+w.Code+`"}`, "X-Forwarded-For", ip); rec.Code != 200 {
			t.Fatalf("pair %d: %d", i, rec.Code)
		}
	}
}

func fakeToken(i int) string {
	return strings.Repeat("a", 56) + fmt.Sprintf("%08x", i)
}

func TestPutRotatingFakeTokensHitIPLimit(t *testing.T) {
	e := newEnv(t)
	for i := 1; i <= 30; i++ {
		if rec := e.do("PUT", "/v1/snapshot", fakeToken(i), goodSnap); rec.Code != 401 {
			t.Fatalf("request %d: %d", i, rec.Code)
		}
	}
	if rec := e.do("PUT", "/v1/snapshot", fakeToken(31), goodSnap); rec.Code != 429 {
		t.Fatalf("31st request: %d", rec.Code)
	}
}

func TestPutMalformedTokenDoesNotConsumeIPBudget(t *testing.T) {
	e := newEnv(t)
	bad := []string{"bogus", strings.Repeat("A", 64), strings.Repeat("a", 63), strings.Repeat("g", 64), strings.Repeat("a", 65)}
	for i := 0; i < 40; i++ {
		if rec := e.do("PUT", "/v1/snapshot", bad[i%len(bad)], goodSnap); rec.Code != 401 {
			t.Fatalf("malformed %d: %d", i, rec.Code)
		}
	}
	tok := pairedHelper(e)
	if rec := e.do("PUT", "/v1/snapshot", tok, goodSnap); rec.Code != 204 {
		t.Fatalf("valid put after malformed flood: %d", rec.Code)
	}
}

func TestPairMalformedHelperToken401(t *testing.T) {
	e := newEnv(t)
	w, _ := e.register()
	for i := 0; i < 40; i++ {
		if rec := e.do("POST", "/v1/pair", "unknowntoken", `{"code":"`+w.Code+`"}`); rec.Code != 401 {
			t.Fatalf("malformed %d: %d", i, rec.Code)
		}
	}
	// The code was never consumed and the IP budget is intact.
	if rec := e.do("POST", "/v1/pair", "", `{"code":"`+w.Code+`"}`); rec.Code != 200 {
		t.Fatalf("pair after malformed attempts: %d", rec.Code)
	}
}

func TestForgetHelper(t *testing.T) {
	e := newEnv(t)
	w, auth := e.register()
	rec := e.do("POST", "/v1/pair", "", `{"code":"`+w.Code+`"}`)
	var pr struct {
		HelperToken string `json:"helper_token"`
	}
	decode(t, rec, &pr)
	if rec = e.do("DELETE", "/v1/helper", pr.HelperToken, ""); rec.Code != 204 {
		t.Fatalf("forget: %d %s", rec.Code, rec.Body)
	}
	rec = e.do("GET", "/v1/watch", auth, "")
	var st watchResp
	decode(t, rec, &st)
	if st.Paired || st.Code == "" {
		t.Fatalf("watch after forget: %s", rec.Body)
	}
	if rec = e.do("DELETE", "/v1/helper", pr.HelperToken, ""); rec.Code != 401 {
		t.Fatalf("second forget: %d", rec.Code)
	}
}

func TestForgetHelperMalformedToken401BeforeLimiter(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 40; i++ {
		if rec := e.do("DELETE", "/v1/helper", "not-a-token", ""); rec.Code != 401 {
			t.Fatalf("malformed #%d: %d", i, rec.Code)
		}
	}
	// the IP budget is untouched: a well-formed unknown token still gets 401, not 429
	if rec := e.do("DELETE", "/v1/helper", strings.Repeat("b", 64), ""); rec.Code != 401 {
		t.Fatalf("unknown token: %d", rec.Code)
	}
}
