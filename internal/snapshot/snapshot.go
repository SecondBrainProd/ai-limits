// Package snapshot defines the compact usage snapshot shared by the relay
// and the desktop helper, and parses the Claude usage response into it.
package snapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
	"unicode/utf8"
)

const (
	// Version is the current snapshot format version.
	Version = 1
	// MaxRows is the maximum number of rows in a snapshot.
	MaxRows = 3
	// maxField is the maximum byte length of Kind and Label.
	maxField = 32
)

// ErrNoLimits is returned when the usage response contains no limits.
var ErrNoLimits = errors.New("usage response has no limits")

// Row is one usage limit.
type Row struct {
	Kind  string `json:"kind"`            // "session" | "weekly_all" | "weekly_scoped" | any future kind
	Label string `json:"label,omitempty"` // scope.model/surface display_name for scoped and unknown kinds
	Pct   int    `json:"pct"`             // 0..100, rounded
	Reset int64  `json:"reset"`           // epoch seconds; 0 if unknown
	Sev   int    `json:"sev"`             // normal 0, warning/high 1, critical 2; unknown 0
}

// Snapshot is the shared wire format.
type Snapshot struct {
	V    int   `json:"v"`
	T    int64 `json:"t"`
	Rows []Row `json:"rows"`
}

type usageWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    *string  `json:"resets_at"`
}

type usageScopeName struct {
	DisplayName string `json:"display_name"`
}

type usageLimit struct {
	Kind     string   `json:"kind"`
	Percent  *float64 `json:"percent"`
	Severity string   `json:"severity"`
	ResetsAt *string  `json:"resets_at"`
	Scope    *struct {
		Model   *usageScopeName `json:"model"`
		Surface *usageScopeName `json:"surface"`
	} `json:"scope"`
}

type usageResponse struct {
	FiveHour *usageWindow `json:"five_hour"`
	SevenDay *usageWindow `json:"seven_day"`
	Limits   []usageLimit `json:"limits"`
}

// FromUsage converts a usage API response body into a Snapshot.
// It prefers the "limits" array and falls back to five_hour/seven_day.
func FromUsage(body []byte, now time.Time) (Snapshot, error) {
	var u usageResponse
	if err := json.Unmarshal(body, &u); err != nil {
		return Snapshot{}, fmt.Errorf("parse usage response: %w", err)
	}
	var rows []Row
	for _, l := range u.Limits {
		if l.Kind == "" || l.Percent == nil {
			continue
		}
		r := Row{
			Kind:  truncate(l.Kind),
			Pct:   clampPct(*l.Percent),
			Reset: parseTime(l.ResetsAt),
			Sev:   severity(l.Severity),
		}
		if l.Scope != nil {
			switch {
			case l.Scope.Model != nil && l.Scope.Model.DisplayName != "":
				r.Label = truncate(l.Scope.Model.DisplayName)
			case l.Scope.Surface != nil && l.Scope.Surface.DisplayName != "":
				r.Label = truncate(l.Scope.Surface.DisplayName)
			}
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		if u.FiveHour != nil && u.FiveHour.Utilization != nil {
			rows = append(rows, windowRow("session", u.FiveHour))
		}
		if u.SevenDay != nil && u.SevenDay.Utilization != nil {
			rows = append(rows, windowRow("weekly_all", u.SevenDay))
		}
	}
	if len(rows) == 0 {
		return Snapshot{}, ErrNoLimits
	}
	if len(rows) > MaxRows {
		rows = rows[:MaxRows]
	}
	return Snapshot{V: Version, T: now.Unix(), Rows: rows}, nil
}

func windowRow(kind string, w *usageWindow) Row {
	return Row{Kind: kind, Pct: clampPct(*w.Utilization), Reset: parseTime(w.ResetsAt)}
}

func clampPct(f float64) int {
	p := int(math.Round(f))
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

func parseTime(s *string) int64 {
	if s == nil || *s == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339Nano, *s)
	if err != nil {
		return 0
	}
	return t.Unix()
}

func severity(s string) int {
	switch s {
	case "warning", "high":
		return 1
	case "critical":
		return 2
	}
	return 0
}

// truncate cuts s to at most maxField bytes on a rune boundary.
func truncate(s string) string {
	if len(s) <= maxField {
		return s
	}
	n := maxField
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Validate checks the snapshot against the shared format rules.
func (s Snapshot) Validate() error {
	if s.V != Version {
		return fmt.Errorf("unsupported snapshot version %d", s.V)
	}
	if s.T <= 0 {
		return fmt.Errorf("invalid timestamp %d", s.T)
	}
	if len(s.Rows) < 1 || len(s.Rows) > MaxRows {
		return fmt.Errorf("rows count %d out of range 1..%d", len(s.Rows), MaxRows)
	}
	for i, r := range s.Rows {
		if r.Kind == "" || len(r.Kind) > maxField {
			return fmt.Errorf("row %d: invalid kind", i)
		}
		if len(r.Label) > maxField {
			return fmt.Errorf("row %d: label too long", i)
		}
		if r.Pct < 0 || r.Pct > 100 {
			return fmt.Errorf("row %d: pct %d out of range", i, r.Pct)
		}
		if r.Sev < 0 || r.Sev > 2 {
			return fmt.Errorf("row %d: sev %d out of range", i, r.Sev)
		}
		if r.Reset < 0 {
			return fmt.Errorf("row %d: negative reset", i)
		}
	}
	return nil
}
