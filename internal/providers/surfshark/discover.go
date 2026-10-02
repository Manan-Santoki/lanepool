package surfshark

import (
	"context"
	"net"
	"net/netip"
	"sort"
	"sync"
	"time"
)

// Each Surfshark location name (e.g. us-nyc.prod.surfshark.com) is served by
// many servers; DNS answers with a couple of them at a time, rotating. Asking
// several resolvers repeatedly finds most of them, and every server IP can be a
// lane of its own (same peer key as its location).

// LookupFunc resolves a host name to IPv4 addresses.
type LookupFunc func(ctx context.Context, host string) ([]netip.Addr, error)

// PublicResolvers are queried in turn by DefaultLookup, plus the system resolver.
var PublicResolvers = []string{"1.1.1.1:53", "8.8.8.8:53", "9.9.9.9:53", "208.67.222.222:53"}

// DefaultLookup returns a LookupFunc that cycles through the system resolver
// and PublicResolvers on successive calls.
func DefaultLookup() LookupFunc {
	resolvers := []*net.Resolver{net.DefaultResolver}
	for _, addr := range PublicResolvers {
		resolvers = append(resolvers, &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		}})
	}
	var mu sync.Mutex
	next := 0
	return func(ctx context.Context, host string) ([]netip.Addr, error) {
		mu.Lock()
		r := resolvers[next%len(resolvers)]
		next++
		mu.Unlock()
		return r.LookupNetIP(ctx, "ip4", host)
	}
}

// DiscoverIPs looks up every host rounds times and returns the distinct IPv4
// addresses seen per host, sorted. Hosts that never resolve are left out.
func DiscoverIPs(ctx context.Context, hosts []string, rounds int, lookup LookupFunc) map[string][]netip.Addr {
	if lookup == nil {
		lookup = DefaultLookup()
	}
	seen := make(map[string]map[netip.Addr]bool, len(hosts))
	var mu sync.Mutex
	sem := make(chan struct{}, 32)
	var wg sync.WaitGroup
	for _, h := range hosts {
		for i := 0; i < rounds; i++ {
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				addrs, err := lookup(lctx, h)
				if err != nil {
					return
				}
				mu.Lock()
				defer mu.Unlock()
				for _, a := range addrs {
					if a = a.Unmap(); a.Is4() {
						if seen[h] == nil {
							seen[h] = map[netip.Addr]bool{}
						}
						seen[h][a] = true
					}
				}
			}()
		}
	}
	wg.Wait()
	out := make(map[string][]netip.Addr, len(seen))
	for h, set := range seen {
		for a := range set {
			out[h] = append(out[h], a)
		}
		sort.Slice(out[h], func(i, j int) bool { return out[h][i].Less(out[h][j]) })
	}
	return out
}

// PoolServer is one server of a location, reached at its own IP.
type PoolServer struct {
	Server
	IP netip.Addr // zero when the location's IPs are unknown: connect by name
}

// ID is the location and server IP, e.g. "us-nyc@146.70.186.133".
func (p PoolServer) ID() string {
	if !p.IP.IsValid() {
		return p.Server.ID()
	}
	return p.Server.ID() + "@" + p.IP.String()
}

// Endpoint is the server's WireGuard endpoint.
func (p PoolServer) Endpoint() string {
	if !p.IP.IsValid() {
		return p.Server.Endpoint()
	}
	return netip.AddrPortFrom(p.IP, 51820).String()
}

// Pool expands locations into their servers, interleaved: the first server of
// every location, then every second one, and so on, so lanes started in order
// spread across locations. Locations without known IPs appear once, by name.
func Pool(servers []Server, ips map[string][]netip.Addr) []PoolServer {
	var out []PoolServer
	for depth := 0; ; depth++ {
		added := false
		for _, s := range servers {
			list := ips[s.ConnectionName]
			switch {
			case depth < len(list):
				out = append(out, PoolServer{Server: s, IP: list[depth]})
			case depth == 0:
				out = append(out, PoolServer{Server: s})
			default:
				continue
			}
			added = true
		}
		if !added {
			return out
		}
	}
}
