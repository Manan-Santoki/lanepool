package gateway

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/Manan-Santoki/lanepool/internal/engine/lanes"
)

type fakeLanes struct {
	host string
	ip   netip.Addr
	err  error
}

func (f *fakeLanes) Usable() []lanes.Info { return nil }
func (f *fakeLanes) Dial(context.Context, string, string) (net.Conn, func(), error) {
	return nil, nil, errors.New("unused")
}
func (f *fakeLanes) ListenPacket(context.Context, string) (net.PacketConn, func(), error) {
	return nil, nil, errors.New("unused")
}
func (f *fakeLanes) LookupHost(_ context.Context, _, host string) (netip.Addr, error) {
	if f.err != nil {
		return netip.Addr{}, f.err
	}
	if host != f.host {
		return netip.Addr{}, errors.New("unexpected host")
	}
	return f.ip, nil
}

func TestResolveUDPAddrUsesCache(t *testing.T) {
	ip := netip.MustParseAddr("198.51.100.53")
	lanes := &fakeLanes{host: "resolver.wgtest", ip: ip}
	cache := newUDPHostCache()
	ctx := context.Background()

	got, err := resolveUDPAddr(ctx, lanes, "lane-1", "resolver.wgtest", cache)
	if err != nil || got != ip {
		t.Fatalf("first resolve: %v %v", got, err)
	}
	lanes.err = errors.New("should not call again")
	got2, err := resolveUDPAddr(ctx, lanes, "lane-1", "resolver.wgtest", cache)
	if err != nil || got2 != ip {
		t.Fatalf("cached resolve: %v %v", got2, err)
	}
}

func TestResolveUDPAddrLiteralIP(t *testing.T) {
	ip := netip.MustParseAddr("1.1.1.1")
	got, err := resolveUDPAddr(context.Background(), &fakeLanes{}, "lane", ip.String(), newUDPHostCache())
	if err != nil || got != ip {
		t.Fatalf("literal: %v %v", got, err)
	}
}

func TestUDPHostCacheTTL(t *testing.T) {
	c := newUDPHostCache()
	ip := netip.MustParseAddr("10.0.0.1")
	c.mu.Lock()
	c.entries["x"] = udpHostCacheEntry{ip: ip, exp: time.Now().Add(-time.Second)}
	c.mu.Unlock()
	if _, ok := c.get("x"); ok {
		t.Fatal("expected expired entry")
	}
}
