package gateway

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Manan-Santoki/lanepool/internal/engine/lanes"
	"github.com/Manan-Santoki/lanepool/internal/protocol"
)

func laneInfos(ids ...string) []lanes.Info {
	out := make([]lanes.Info, len(ids))
	for i, id := range ids {
		out[i] = lanes.Info{ID: id}
	}
	return out
}

func roundRobin() func([]lanes.Info) string {
	var rr atomic.Uint64
	return func(c []lanes.Info) string { return pick(c, protocol.StrategyRoundRobin, &rr) }
}

func without(c []lanes.Info, id string) []lanes.Info {
	var out []lanes.Info
	for _, l := range c {
		if l.ID != id {
			out = append(out, l)
		}
	}
	return out
}

func TestStickySessionsGetDistinctLanes(t *testing.T) {
	p := NewPolicy()
	cands := laneInfos("a", "b", "c", "d")
	choose := roundRobin()

	got := map[string]string{}
	used := map[string]bool{}
	for _, s := range []string{"s1", "s2", "s3"} {
		id := p.stickyPick("app", "s|"+s, time.Hour, cands, choose)
		if used[id] {
			t.Fatalf("session %s shares lane %s: %v", s, id, got)
		}
		used[id], got[s] = true, id
	}
	if id := p.stickyPick("app", "s|s2", time.Hour, cands, choose); id != got["s2"] {
		t.Fatalf("sticky session moved from %s to %s", got["s2"], id)
	}

	// s2's lane goes down: s2 must move to the one lane nobody holds, not onto
	// s1's or s3's lane, though round robin alone would land there.
	var free string
	for _, c := range cands {
		if !used[c.ID] {
			free = c.ID
		}
	}
	if id := p.stickyPick("app", "s|s2", time.Hour, without(cands, got["s2"]), choose); id != free {
		t.Fatalf("s2 moved to %s, want the free lane %s (held: %v)", id, free, got)
	}

	// Another user's sessions don't count as held.
	if id := p.stickyPick("other", "s|x", time.Hour, laneInfos(got["s1"]), choose); id != got["s1"] {
		t.Fatalf("other user got %s", id)
	}
}

func TestStickySessionsShareOnlyWhenEveryLaneIsHeld(t *testing.T) {
	p := NewPolicy()
	cands := laneInfos("a", "b")
	choose := roundRobin()
	p.stickyPick("app", "s|1", time.Hour, cands, choose)
	p.stickyPick("app", "s|2", time.Hour, cands, choose)
	if id := p.stickyPick("app", "s|3", time.Hour, cands, choose); id == "" {
		t.Fatal("no lane when every lane is held")
	}
}

func TestExpiredAndForgottenSessionsReleaseLanes(t *testing.T) {
	p := NewPolicy()
	cands := laneInfos("a", "b")
	choose := roundRobin()
	first := p.stickyPick("app", "s|old", time.Nanosecond, cands, choose)
	time.Sleep(time.Millisecond)
	// The expired session no longer holds its lane, so two new sessions can use both lanes.
	a := p.stickyPick("app", "s|n1", time.Hour, cands, choose)
	b := p.stickyPick("app", "s|n2", time.Hour, cands, choose)
	if a == b {
		t.Fatalf("both sessions on %s (expired session on %s still held)", a, first)
	}
	p.ForgetSticky("APP", "n1")
	if c := p.stickyPick("app", "s|n3", time.Hour, cands, choose); c != a {
		t.Fatalf("forgotten session's lane %s not reused, got %s", a, c)
	}
}

func TestConcurrentNewSessionsGetDistinctLanes(t *testing.T) {
	p := NewPolicy()
	ids := make([]string, 30)
	for i := range ids {
		ids[i] = fmt.Sprintf("l%d", i)
	}
	cands := laneInfos(ids...)
	choose := roundRobin()
	var mu sync.Mutex
	seen := map[string]string{}
	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := p.stickyPick("app", fmt.Sprintf("s|lgw%02d", i), time.Hour, cands, choose)
			mu.Lock()
			defer mu.Unlock()
			if prev, dup := seen[id]; dup {
				t.Errorf("sessions %s and lgw%02d share lane %s", prev, i, id)
			}
			seen[id] = fmt.Sprintf("lgw%02d", i)
		}(i)
	}
	wg.Wait()
}

// End to end through the proxy: three sessions on three lanes see three exit IPs.
func TestProxySessionsGetDistinctExitIPs(t *testing.T) {
	e := newEnv(t, "us", "us", "us")
	e.addUser(protocol.UserSpec{Username: "app", Enabled: true}, "password123")
	seen := map[string]string{}
	for _, s := range []string{"a", "b", "c"} {
		ip, err := e.socksGet("app-session-"+s, "password123")
		if err != nil {
			t.Fatal(err)
		}
		if prev, dup := seen[ip]; dup {
			t.Fatalf("sessions %s and %s share exit %s", prev, s, ip)
		}
		seen[ip] = s
	}
}
