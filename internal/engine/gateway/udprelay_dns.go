package gateway

import (
	"context"
	"log/slog"
	"net/netip"
	"sync"
	"time"
)

// UDP replies use the resolved upstream IP in the SOCKS5 header (RFC 1928 source
// address). Clients that sent a domain ATYP still receive IPv4/IPv6 in replies.

const (
	udpHostCacheMinTTL = 60 * time.Second
	udpHostCacheMaxTTL = 300 * time.Second
)

type udpHostCache struct {
	mu      sync.Mutex
	entries map[string]udpHostCacheEntry
}

type udpHostCacheEntry struct {
	ip  netip.Addr
	exp time.Time
}

func newUDPHostCache() *udpHostCache {
	return &udpHostCache{entries: map[string]udpHostCacheEntry{}}
}

func (c *udpHostCache) get(host string) (netip.Addr, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[host]
	if !ok || time.Now().After(e.exp) {
		if ok {
			delete(c.entries, host)
		}
		return netip.Addr{}, false
	}
	return e.ip, true
}

func (c *udpHostCache) put(host string, ip netip.Addr, ttl time.Duration) {
	if ttl < udpHostCacheMinTTL {
		ttl = udpHostCacheMinTTL
	}
	if ttl > udpHostCacheMaxTTL {
		ttl = udpHostCacheMaxTTL
	}
	c.mu.Lock()
	c.entries[host] = udpHostCacheEntry{ip: ip, exp: time.Now().Add(ttl)}
	c.mu.Unlock()
}

func resolveUDPAddr(ctx context.Context, lanes Lanes, laneID string, host string, cache *udpHostCache) (netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip, nil
	}
	if cache != nil {
		if ip, ok := cache.get(host); ok {
			return ip, nil
		}
	}
	ip, err := lanes.LookupHost(ctx, laneID, host)
	if err != nil {
		return netip.Addr{}, err
	}
	if cache != nil {
		cache.put(host, ip, udpHostCacheMaxTTL)
	}
	return ip, nil
}

func (s *Server) logUDPDrop(log *slog.Logger, reason string, err error) {
	s.udpDrops.Add(1)
	if log != nil {
		if err != nil {
			log.Debug("socks5 udp relay drop", "reason", reason, "err", err)
		} else {
			log.Debug("socks5 udp relay drop", "reason", reason)
		}
	}
}
