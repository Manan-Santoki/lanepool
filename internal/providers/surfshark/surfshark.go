// Package surfshark turns Surfshark's public server list into lane definitions.
package surfshark

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/Manan-Santoki/lanepool/internal/wg"
)

// DefaultAPI is Surfshark's public list of server locations.
const DefaultAPI = "https://api.surfshark.com/v4/server/clusters/generic"

// Every Surfshark WireGuard client uses the same tunnel address and DNS servers;
// the server NATs it.
var (
	TunnelAddress = netip.MustParseAddr("10.14.0.2")
	DNS           = []netip.Addr{netip.MustParseAddr("162.252.172.57"), netip.MustParseAddr("149.154.159.92")}
)

// Server is one Surfshark location.
type Server struct {
	Country        string   `json:"country"`
	CountryCode    string   `json:"countryCode"`
	Location       string   `json:"location"`
	ConnectionName string   `json:"connectionName"` // e.g. us-nyc.prod.surfshark.com
	PubKey         string   `json:"pubKey"`
	Load           int      `json:"load"`
	Tags           []string `json:"tags"`
}

// ID is the short location name, e.g. "us-nyc".
func (s Server) ID() string {
	id, _, _ := strings.Cut(strings.ToLower(s.ConnectionName), ".")
	return id
}

// Virtual reports whether the location is hosted in another country.
func (s Server) Virtual() bool {
	for _, t := range s.Tags {
		if t == "virtual" {
			return true
		}
	}
	return false
}

// Endpoint is the WireGuard endpoint of the location.
func (s Server) Endpoint() string { return s.ConnectionName + ":51820" }

// Fetch downloads the server list.
func Fetch(ctx context.Context, url string) ([]Server, error) {
	if url == "" {
		url = DefaultAPI
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "lanepool")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("surfshark server list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("surfshark server list: HTTP %d", resp.StatusCode)
	}
	var all []Server
	if err := json.NewDecoder(resp.Body).Decode(&all); err != nil {
		return nil, fmt.Errorf("surfshark server list: %w", err)
	}
	servers := all[:0]
	for _, s := range all {
		if s.ConnectionName != "" && wg.ValidKey(s.PubKey) {
			servers = append(servers, s)
		}
	}
	return servers, nil
}

// Filter limits which locations are used.
type Filter struct {
	Locations        []string // pinned location IDs: always used, in this order
	ExcludeLocations []string // never used unless pinned
	Countries        []string // ISO codes to fill from (empty = all)
	ExcludeCountries []string
	IncludeVirtual   bool
}

// Select picks servers: every pinned location first (even beyond limit), then
// more locations from the country filters until limit is reached.
//
// Filled locations are spread across countries: every country's first
// location, then every country's second, and so on. Ordering is alphabetical,
// not by load, so lanes keep their order across restarts.
func Select(servers []Server, f Filter, limit int) []Server {
	byID := make(map[string]Server, len(servers))
	for _, s := range servers {
		byID[s.ID()] = s
	}
	var out []Server
	used := map[string]bool{}
	for _, id := range f.Locations {
		id = strings.ToLower(strings.TrimSpace(id))
		if s, ok := byID[id]; ok && !used[id] {
			out = append(out, s)
			used[id] = true
		}
	}
	excludeLoc := toSet(f.ExcludeLocations)
	include := toSet(f.Countries)
	exclude := toSet(f.ExcludeCountries)
	perCountry := map[string][]Server{}
	for _, s := range servers {
		cc := strings.ToLower(s.CountryCode)
		if used[s.ID()] || excludeLoc[s.ID()] || len(include) > 0 && !include[cc] || exclude[cc] || (!f.IncludeVirtual && s.Virtual()) {
			continue
		}
		perCountry[cc] = append(perCountry[cc], s)
	}
	codes := make([]string, 0, len(perCountry))
	for cc, list := range perCountry {
		sort.Slice(list, func(i, j int) bool { return list[i].ID() < list[j].ID() })
		codes = append(codes, cc)
	}
	sort.Strings(codes)

	for depth := 0; len(out) < limit; depth++ {
		added := false
		for _, cc := range codes {
			if depth < len(perCountry[cc]) && len(out) < limit {
				out = append(out, perCountry[cc][depth])
				added = true
			}
		}
		if !added {
			break
		}
	}
	return out
}

// TunnelConfig is the WireGuard configuration for connecting to s with privateKey.
func TunnelConfig(s Server, privateKey string) wg.Config {
	return wg.Config{
		PrivateKey: privateKey,
		Addresses:  []netip.Addr{TunnelAddress},
		DNS:        DNS,
		PeerKey:    s.PubKey,
		Endpoint:   s.Endpoint(),
	}
}

func toSet(list []string) map[string]bool {
	m := map[string]bool{}
	for _, v := range list {
		if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
			m[v] = true
		}
	}
	return m
}
