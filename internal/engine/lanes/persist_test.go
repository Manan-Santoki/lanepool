package lanes

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Manan-Santoki/lanepool/internal/protocol"
)

var quietLog = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestRememberedLanesGoFirstInStartupBurst(t *testing.T) {
	s := poolSettings()
	s.TargetUp = 2
	h := newPoolHarness(t, s, poolSpecs("p0", "p1", "p2", "p3", "p4", "p5"))
	h.m.Remember([]string{"p4", "p5"}) // worked before the restart
	h.tick(t, 0)
	for _, id := range []string{"p4", "p5"} {
		if state(h.m, id).Status != protocol.LaneUp {
			t.Fatalf("remembered lane %s not in the first burst: %+v", id, h.m.States())
		}
	}
	if h.starts.Load() != 2 {
		t.Fatalf("burst started %d servers, want only the 2 remembered", h.starts.Load())
	}
}

func TestKnownGoodSurvivesRestartThroughFile(t *testing.T) {
	s := poolSettings()
	s.TargetUp = 2
	specs := poolSpecs("a", "b", "c", "d")
	h := newPoolHarness(t, s, specs)
	h.tick(t, 0)
	known := h.m.KnownGood(10)
	if len(known) != 2 {
		t.Fatalf("known good %v", known)
	}

	path := filepath.Join(t.TempDir(), "known-lanes.json")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // saves once on the way out
	SaveKnownGood(ctx, h.m, path, time.Hour, quietLog)

	next := New(s, nil)
	LoadKnownGood(next, path, quietLog)
	next.Apply(s, specs)
	got := next.KnownGood(10)
	slices.Sort(got)
	slices.Sort(known)
	if !slices.Equal(got, known) {
		t.Fatalf("restored %v, want %v", got, known)
	}
}

func TestLoadKnownGoodToleratesMissingAndBadFiles(t *testing.T) {
	dir := t.TempDir()
	m := New(poolSettings(), nil)
	LoadKnownGood(m, filepath.Join(dir, "missing.json"), quietLog)
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte("{not json"), 0o600)
	LoadKnownGood(m, bad, quietLog)
	m.Apply(poolSettings(), poolSpecs("x"))
	if len(m.KnownGood(10)) != 0 {
		t.Fatal("lanes marked known from a missing or bad file")
	}
}
