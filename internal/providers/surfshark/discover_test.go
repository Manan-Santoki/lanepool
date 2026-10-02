package surfshark

import (
	"context"
	"net/netip"
	"sync/atomic"
	"testing"
)

func TestDiscoverIPsCollectsRotatingAnswers(t *testing.T) {
	var n atomic.Int64
	lookup := func(_ context.Context, host string) ([]netip.Addr, error) {
		i := n.Add(1)
		if host == "b.prod.surfshark.com" {
			return []netip.Addr{netip.MustParseAddr("192.0.2.9")}, nil
		}
		// a rotates through four servers, two per answer.
		return []netip.Addr{netip.AddrFrom4([4]byte{198, 51, 100, byte(i % 4)}), netip.AddrFrom4([4]byte{198, 51, 100, byte((i + 1) % 4)})}, nil
	}
	got := DiscoverIPs(context.Background(), []string{"a.prod.surfshark.com", "b.prod.surfshark.com"}, 8, lookup)
	if len(got["a.prod.surfshark.com"]) != 4 || len(got["b.prod.surfshark.com"]) != 1 {
		t.Fatalf("got %v", got)
	}
}

func TestPoolInterleavesLocations(t *testing.T) {
	a := Server{ConnectionName: "aa-one.prod.surfshark.com"}
	b := Server{ConnectionName: "bb-two.prod.surfshark.com"}
	c := Server{ConnectionName: "cc-three.prod.surfshark.com"}
	ips := map[string][]netip.Addr{
		a.ConnectionName: {netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("192.0.2.2")},
		b.ConnectionName: {netip.MustParseAddr("192.0.2.3")},
	}
	var ids []string
	for _, p := range Pool([]Server{a, b, c}, ips) {
		ids = append(ids, p.ID()+" "+p.Endpoint())
	}
	want := []string{
		"aa-one@192.0.2.1 192.0.2.1:51820",
		"bb-two@192.0.2.3 192.0.2.3:51820",
		"cc-three cc-three.prod.surfshark.com:51820",
		"aa-one@192.0.2.2 192.0.2.2:51820",
	}
	if len(ids) != len(want) {
		t.Fatalf("got %v", ids)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("got %v, want %v", ids, want)
		}
	}
}
