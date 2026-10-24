package snapshot

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

func mustParse(t *testing.T, s string) int64 {
	t.Helper()
	ts, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts.Unix()
}

func TestFromUsageLimits(t *testing.T) {
	body, err := os.ReadFile("testdata/usage-20261022.json")
	if err != nil {
		t.Fatal(err)
	}
	s, err := FromUsage(body, now)
	if err != nil {
		t.Fatal(err)
	}
	if s.V != Version || s.T != now.Unix() {
		t.Fatalf("bad header: %+v", s)
	}
	if len(s.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(s.Rows))
	}
	if s.Rows[0].Kind != "session" || s.Rows[0].Pct != 2 {
		t.Errorf("row0 = %+v", s.Rows[0])
	}
	if s.Rows[1].Kind != "weekly_all" || s.Rows[1].Pct != 71 {
		t.Errorf("row1 = %+v", s.Rows[1])
	}
	r := s.Rows[2]
	if r.Kind != "weekly_scoped" || r.Label != "Fable" || r.Sev != 2 || r.Pct != 99 {
		t.Errorf("row2 = %+v", r)
	}
	if want := mustParse(t, "2026-10-23T21:00:00.489777+00:00"); r.Reset != want {
		t.Errorf("reset = %d, want %d", r.Reset, want)
	}
}

func TestFromUsageFallback(t *testing.T) {
	body := `{"five_hour":{"utilization":25.4,"resets_at":"2026-10-22T09:40:00+00:00"},"seven_day":{"utilization":69,"resets_at":null}}`
	s, err := FromUsage([]byte(body), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Rows) != 2 {
		t.Fatalf("rows = %d", len(s.Rows))
	}
	if s.Rows[0].Kind != "session" || s.Rows[0].Pct != 25 || s.Rows[0].Reset != mustParse(t, "2026-10-22T09:40:00+00:00") {
		t.Errorf("row0 = %+v", s.Rows[0])
	}
	if s.Rows[1].Kind != "weekly_all" || s.Rows[1].Pct != 69 || s.Rows[1].Reset != 0 {
		t.Errorf("row1 = %+v", s.Rows[1])
	}
}

func TestFromUsageExtraRows(t *testing.T) {
	body := `{"limits":[
		{"kind":"a","percent":1},{"kind":"b","percent":2},
		{"kind":"c","percent":3},{"kind":"d","percent":4}]}`
	s, err := FromUsage([]byte(body), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Rows) != 3 || s.Rows[0].Kind != "a" || s.Rows[1].Kind != "b" || s.Rows[2].Kind != "c" {
		t.Errorf("rows = %+v", s.Rows)
	}
}

func TestFromUsageUnknownKind(t *testing.T) {
	body := `{"limits":[{"kind":"weekly_surface","percent":40,"severity":"purple",
		"resets_at":null,"scope":{"model":null,"surface":{"display_name":"Desktop"}}}]}`
	s, err := FromUsage([]byte(body), now)
	if err != nil {
		t.Fatal(err)
	}
	r := s.Rows[0]
	if r.Label != "Desktop" || r.Sev != 0 || r.Reset != 0 {
		t.Errorf("row = %+v", r)
	}
}

func TestFromUsageEmpty(t *testing.T) {
	if _, err := FromUsage([]byte(`{}`), now); !errors.Is(err, ErrNoLimits) {
		t.Fatalf("err = %v, want ErrNoLimits", err)
	}
}

func TestFromUsageBadJSON(t *testing.T) {
	if _, err := FromUsage([]byte(`nope`), now); err == nil {
		t.Fatal("want error")
	}
}

func TestValidate(t *testing.T) {
	ok := Row{Kind: "session", Pct: 5}
	cases := map[string]Snapshot{
		"zero rows":    {V: Version, T: 1},
		"four rows":    {V: Version, T: 1, Rows: []Row{ok, ok, ok, ok}},
		"pct 101":      {V: Version, T: 1, Rows: []Row{{Kind: "session", Pct: 101}}},
		"pct negative": {V: Version, T: 1, Rows: []Row{{Kind: "session", Pct: -1}}},
		"empty kind":   {V: Version, T: 1, Rows: []Row{{Pct: 5}}},
		"long kind":    {V: Version, T: 1, Rows: []Row{{Kind: strings.Repeat("k", 33)}}},
		"long label":   {V: Version, T: 1, Rows: []Row{{Kind: "k", Label: strings.Repeat("l", 33)}}},
		"bad version":  {V: 2, T: 1, Rows: []Row{ok}},
		"sev 3":        {V: Version, T: 1, Rows: []Row{{Kind: "k", Sev: 3}}},
		"sev negative": {V: Version, T: 1, Rows: []Row{{Kind: "k", Sev: -1}}},
		"reset neg":    {V: Version, T: 1, Rows: []Row{{Kind: "k", Reset: -1}}},
		"t zero":       {V: Version, T: 0, Rows: []Row{{Kind: "k"}}},
		"t negative":   {V: Version, T: -5, Rows: []Row{{Kind: "k"}}},
	}
	// Every case is valid except for its own defect (T: 1 everywhere), so each one
	// exercises the check it is named after.
	if err := (Snapshot{V: Version, T: 1, Rows: []Row{ok}}).Validate(); err != nil {
		t.Fatalf("valid base snapshot: %v", err)
	}
	for name, s := range cases {
		if err := s.Validate(); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	body, err := os.ReadFile("testdata/usage-20261022.json")
	if err != nil {
		t.Fatal(err)
	}
	s, err := FromUsage(body, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(); err != nil {
		t.Errorf("fixture: %v", err)
	}
}
