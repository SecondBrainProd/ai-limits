//go:build live

package usage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/SecondBrainProd/ai-limits/helper/creds"
	"github.com/SecondBrainProd/ai-limits/internal/snapshot"
)

// TestLive reads the real local token and the real endpoint. Run manually:
// go test -tags live ./helper/usage/ -run Live -v
// It prints only kind and pct of each row, never the token or the raw body.
func TestLive(t *testing.T) {
	tok, err := creds.Default().Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	body, err := Client{Version: "0.1"}.Fetch(ctx, tok.Access)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := snapshot.FromUsage(body, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range snap.Rows {
		fmt.Printf("kind=%s pct=%v\n", r.Kind, r.Pct)
	}
}
