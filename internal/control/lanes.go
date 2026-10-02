package control

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/Manan-Santoki/lanepool/internal/protocol"
	"github.com/Manan-Santoki/lanepool/internal/providers/surfshark"
)

// Lane is a lane as the dashboard sees it: definition plus runtime state.
type Lane struct {
	ID                string     `json:"id"`
	Name              string     `json:"name"`
	Provider          string     `json:"provider"`
	Country           string     `json:"country,omitempty"`
	CountryCode       string     `json:"countryCode,omitempty"`
	City              string     `json:"city,omitempty"`
	Virtual           bool       `json:"virtual,omitempty"`
	Enabled           bool       `json:"enabled"`
	Status            string     `json:"status"`
	KeyID             int64      `json:"keyId,omitempty"`
	KeyLabel          string     `json:"keyLabel,omitempty"`
	ExitIP            string     `json:"exitIp,omitempty"`
	LatencyMs         int        `json:"latencyMs,omitempty"`
	LastHandshake     *time.Time `json:"lastHandshake,omitempty"`
	ActiveConnections int        `json:"activeConnections"`
	RxBytes           uint64     `json:"rxBytes"`
	TxBytes           uint64     `json:"txBytes"`
	Restarts          int        `json:"restarts"`
	LastError         string     `json:"lastError,omitempty"`
	NextRetry         *time.Time `json:"nextRetry,omitempty"`
}

type laneRow struct {
	ID, Provider, Name, Country, CountryCode, City, Endpoint, PeerKey string
	Virtual, Enabled                                                  bool
}

func (s *Server) activeLanes(ctx context.Context) ([]laneRow, error) {
	rows, err := s.db.Query(ctx, `SELECT id, provider, name, country, country_code, city, endpoint, peer_key, virtual, enabled
		FROM lanes WHERE active ORDER BY position, id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (laneRow, error) {
		var l laneRow
		err := row.Scan(&l.ID, &l.Provider, &l.Name, &l.Country, &l.CountryCode, &l.City, &l.Endpoint, &l.PeerKey, &l.Virtual, &l.Enabled)
		return l, err
	})
}

// listLanesData merges lane definitions with the engine's latest state.
func (s *Server) listLanesData(ctx context.Context) ([]Lane, error) {
	rows, err := s.activeLanes(ctx)
	if err != nil {
		return nil, err
	}
	labels, err := s.keyLabels(ctx)
	if err != nil {
		return nil, err
	}
	sel, err := s.selection(ctx)
	if err != nil {
		return nil, err
	}
	// In a server pool, lanes the engine doesn't report haven't been needed yet.
	idle := protocol.LaneQueued
	if sel.AllServers {
		idle = protocol.LaneStandby
	}
	states, _ := s.latestLaneStates()
	out := make([]Lane, 0, len(rows))
	for _, r := range rows {
		l := Lane{ID: r.ID, Name: r.Name, Provider: r.Provider, Country: r.Country, CountryCode: r.CountryCode,
			City: r.City, Virtual: r.Virtual, Enabled: r.Enabled, Status: idle}
		if !r.Enabled {
			l.Status = protocol.LaneDisabled
		}
		if st, ok := states[r.ID]; ok {
			l.Status, l.KeyID, l.ExitIP, l.LatencyMs = st.Status, st.KeyID, st.ExitIP, st.LatencyMs
			l.LastHandshake, l.ActiveConnections, l.RxBytes, l.TxBytes = st.LastHandshake, st.ActiveConnections, st.RxBytes, st.TxBytes
			l.Restarts, l.LastError, l.NextRetry = st.Restarts, st.LastError, st.NextRetry
			l.KeyLabel = labels[st.KeyID]
		}
		out = append(out, l)
	}
	return out, nil
}

func (s *Server) listLanes(_ http.ResponseWriter, r *http.Request) (any, error) {
	return s.listLanesData(r.Context())
}

func laneParam(r *http.Request) string {
	id, _ := url.PathUnescape(chi.URLParam(r, "lane"))
	return id
}

func (s *Server) patchLane(_ http.ResponseWriter, r *http.Request) (any, error) {
	id := laneParam(r)
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if in.Enabled == nil {
		return nil, errFields(map[string]string{"enabled": "required"})
	}
	ctx := r.Context()
	tag, err := s.db.Exec(ctx, `UPDATE lanes SET enabled = $2, updated_at = now() WHERE id = $1`, id, *in.Enabled)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, errNotFound
	}
	verb := "disabled"
	if *in.Enabled {
		verb = "enabled"
	}
	s.audit(ctx, who(r), "admin.lane_"+verb, verb+" lane "+id)
	s.configChanged()
	lanes, err := s.listLanesData(ctx)
	if err != nil {
		return nil, err
	}
	for _, l := range lanes {
		if l.ID == id {
			return l, nil
		}
	}
	return nil, errNotFound
}

// addLanes adds Surfshark locations as lanes: they are pinned (always used),
// removed from the exclusions, and the lane count grows so no existing lane is
// displaced.
func (s *Server) addLanes(_ http.ResponseWriter, r *http.Request) (any, error) {
	var in struct {
		Locations []string `json:"locations"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	servers, err := s.surfsharkServers(ctx, false)
	if err != nil && len(servers) == 0 {
		return nil, errStatus(http.StatusFailedDependency, err.Error())
	}
	known := map[string]bool{}
	for _, sv := range servers {
		known[sv.ID()] = true
	}
	sel, err := s.selection(ctx)
	if err != nil {
		return nil, err
	}
	active := map[string]bool{}
	rows, err := s.activeLanes(ctx)
	if err != nil {
		return nil, err
	}
	for _, l := range rows {
		active[l.ID] = true
	}
	var added []string
	for _, loc := range in.Locations {
		loc = strings.ToLower(strings.TrimSpace(loc))
		if !known[loc] {
			return nil, errFields(map[string]string{"locations": "unknown location " + loc})
		}
		sel.ExcludeLocations = without(sel.ExcludeLocations, loc)
		if !contains(sel.Locations, loc) {
			sel.Locations = append(sel.Locations, loc)
		}
		if !active["surfshark:"+loc] {
			added = append(added, loc)
		}
	}
	if len(in.Locations) == 0 {
		return nil, errFields(map[string]string{"locations": "choose at least one location"})
	}
	sel.Lanes += len(added)
	if err := s.putSetting(ctx, "surfshark.selection", sel); err != nil {
		return nil, err
	}
	syncErr := s.syncLanes(ctx)
	s.audit(ctx, who(r), "admin.lanes_added", "added lanes: "+strings.Join(in.Locations, ", "))
	if syncErr != nil {
		return nil, errStatus(http.StatusFailedDependency, syncErr.Error())
	}
	return s.listLanesData(ctx)
}

// removeLane removes a lane. Surfshark locations are unpinned and excluded so
// they aren't picked again (and the lane count shrinks so no other location
// replaces it); WireGuard lanes delete their config.
func (s *Server) removeLane(_ http.ResponseWriter, r *http.Request) (any, error) {
	id := laneParam(r)
	ctx := r.Context()
	provider, name, ok := strings.Cut(id, ":")
	if !ok {
		return nil, errNotFound
	}
	switch provider {
	case "surfshark":
		if strings.Contains(name, "@") {
			// One server of a pool: switch it off; its location stays in the pool.
			tag, err := s.db.Exec(ctx, `UPDATE lanes SET enabled = false, updated_at = now() WHERE id = $1 AND active`, id)
			if err != nil {
				return nil, err
			}
			if tag.RowsAffected() == 0 {
				return nil, errNotFound
			}
			s.configChanged()
			s.audit(ctx, who(r), "admin.lane_removed", "removed server "+id+" from the pool")
			return nil, nil
		}
		sel, err := s.selection(ctx)
		if err != nil {
			return nil, err
		}
		var exists bool
		if err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM lanes WHERE id = $1 AND active)`, id).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, errNotFound
		}
		sel.Locations = without(sel.Locations, name)
		if !contains(sel.ExcludeLocations, name) {
			sel.ExcludeLocations = append(sel.ExcludeLocations, name)
		}
		sel.Lanes = max(0, sel.Lanes-1)
		if err := s.putSetting(ctx, "surfshark.selection", sel); err != nil {
			return nil, err
		}
	case "wireguard":
		tag, err := s.db.Exec(ctx, `DELETE FROM wireguard_configs WHERE 'wireguard:' || id = $1`, id)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 0 {
			return nil, errNotFound
		}
	default:
		return nil, errNotFound
	}
	if err := s.syncLanes(ctx); err != nil {
		s.log.Warn("lane sync after removing a lane", "err", err)
	}
	s.audit(ctx, who(r), "admin.lane_removed", "removed lane "+id)
	return nil, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func without(list []string, v string) []string {
	out := []string{}
	for _, x := range list {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func (s *Server) restartLane(_ http.ResponseWriter, r *http.Request) (any, error) {
	id := laneParam(r)
	if err := s.engine.post(r.Context(), "/v1/lanes/"+url.PathEscape(id)+"/restart", nil, nil); err != nil {
		return nil, engineErr(err)
	}
	s.audit(r.Context(), who(r), "admin.lane_restart", "restarted lane "+id)
	return status(http.StatusAccepted), nil
}

func (s *Server) restartAllLanes(_ http.ResponseWriter, r *http.Request) (any, error) {
	if err := s.engine.post(r.Context(), "/v1/lanes/restart-all", nil, nil); err != nil {
		return nil, engineErr(err)
	}
	s.audit(r.Context(), who(r), "admin.lanes_restart_all", "restarted all lanes (paced)")
	return status(http.StatusAccepted), nil
}

func (s *Server) randomLane(_ http.ResponseWriter, r *http.Request) (any, error) {
	lanes, err := s.listLanesData(r.Context())
	if err != nil {
		return nil, err
	}
	country := strings.ToLower(r.URL.Query().Get("country"))
	var up []Lane
	for _, l := range lanes {
		if l.Status == protocol.LaneUp && (country == "" || strings.ToLower(l.CountryCode) == country) {
			up = append(up, l)
		}
	}
	if len(up) == 0 {
		return nil, errStatus(http.StatusServiceUnavailable, "no healthy lane")
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(up))))
	return up[n.Int64()], nil
}

// --- building lanes from providers -------------------------------------------

// maxKeysPerLane bounds the keys sent per lane, keeping the engine config small
// for server pools with thousands of lanes.
const maxKeysPerLane = 3

// laneSpecs returns the engine's lane definitions, including the keys each
// lane may use.
func (s *Server) laneSpecs(ctx context.Context) ([]protocol.LaneSpec, error) {
	rows, err := s.activeLanes(ctx)
	if err != nil {
		return nil, err
	}
	keys, err := s.enabledKeys(ctx)
	if err != nil {
		return nil, err
	}
	wgs, err := s.wireguardSpecs(ctx)
	if err != nil {
		return nil, err
	}
	sel, err := s.selection(ctx)
	if err != nil {
		return nil, err
	}
	var out []protocol.LaneSpec
	i := 0
	for _, r := range rows {
		spec := protocol.LaneSpec{ID: r.ID, Name: r.Name, Provider: r.Provider, Country: r.Country,
			CountryCode: r.CountryCode, City: r.City, Virtual: r.Virtual, Enabled: r.Enabled}
		switch r.Provider {
		case "surfshark":
			spec.Endpoint, spec.PeerKey = r.Endpoint, r.PeerKey
			spec.Addresses = []string{surfshark.TunnelAddress.String()}
			for _, d := range surfshark.DNS {
				spec.DNS = append(spec.DNS, d.String())
			}
			// One key for every lane (the default): spec.Keys is just the first
			// key. Spread: lane i starts with key i mod n and may fall back to
			// the next two. Order changes when keys are added; the engine keeps
			// lanes on their current key unless it was removed.
			if n := len(keys); n > 0 && !sel.SpreadKeys {
				spec.Keys = keys[:1]
			} else if n > 0 {
				for k := 0; k < min(n, maxKeysPerLane); k++ {
					spec.Keys = append(spec.Keys, keys[(i+k)%n])
				}
			}
			i++
		case "wireguard":
			w, ok := wgs[r.ID]
			if !ok {
				continue
			}
			spec.Endpoint, spec.PeerKey, spec.PresharedKey = w.Endpoint, w.PeerKey, w.PresharedKey
			spec.Addresses, spec.DNS, spec.MTU, spec.Keys = w.Addresses, w.DNS, w.MTU, w.Keys
		}
		out = append(out, spec)
	}
	return out, nil
}

// syncLanes rebuilds the lanes table from the Surfshark selection and the
// WireGuard configs. Lanes keep their row (and enabled flag) when they stay
// selected.
func (s *Server) syncLanes(ctx context.Context) error {
	sel, err := s.selection(ctx)
	if err != nil {
		return err
	}
	var keys int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM provider_keys WHERE provider = 'surfshark' AND enabled`).Scan(&keys); err != nil {
		return err
	}
	// Surfshark lanes only exist once there is a key to connect with. If the
	// server list can't be fetched, the current Surfshark lanes are kept as they
	// are and the other providers still sync.
	var selected []surfshark.PoolServer
	syncSurfshark := true
	var fetchErr error
	if sel.Lanes > 0 && keys > 0 {
		servers, err := s.surfsharkServers(ctx, false)
		if err != nil && len(servers) == 0 {
			syncSurfshark, fetchErr = false, err
		}
		limit := sel.Lanes
		if sel.AllServers {
			limit = len(servers)
		}
		locations := surfshark.Select(servers, surfshark.Filter{
			Locations: sel.Locations, ExcludeLocations: sel.ExcludeLocations, Countries: sel.Countries,
			ExcludeCountries: sel.ExcludeCountries, IncludeVirtual: sel.IncludeVirtual,
		}, limit)
		var ips map[string][]netip.Addr
		if sel.AllServers {
			ips = s.poolIPs(ctx, locations)
		}
		selected = surfshark.Pool(locations, ips)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if syncSurfshark {
		if _, err := tx.Exec(ctx, `UPDATE lanes SET active = false WHERE provider = 'surfshark'`); err != nil {
			return err
		}
	}
	if len(selected) > 0 {
		n := len(selected)
		ids, names, countries, codes, cities := make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
		virtual, endpoints, peers, positions := make([]bool, n), make([]string, n), make([]string, n), make([]int32, n)
		for i, srv := range selected {
			ids[i], names[i], countries[i] = "surfshark:"+srv.ID(), srv.ID(), srv.Country
			codes[i], cities[i], virtual[i] = strings.ToUpper(srv.CountryCode), srv.Location, srv.Virtual()
			endpoints[i], peers[i], positions[i] = srv.Endpoint(), srv.PubKey, int32(i)
		}
		_, err := tx.Exec(ctx, `INSERT INTO lanes (id, provider, name, country, country_code, city, virtual, endpoint, peer_key, position, active)
			SELECT id, 'surfshark', name, country, cc, city, virtual, endpoint, peer, pos, true
			FROM unnest($1::text[], $2::text[], $3::text[], $4::text[], $5::text[], $6::bool[], $7::text[], $8::text[], $9::int[])
				AS t(id, name, country, cc, city, virtual, endpoint, peer, pos)
			ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, country = EXCLUDED.country, country_code = EXCLUDED.country_code,
				city = EXCLUDED.city, virtual = EXCLUDED.virtual, endpoint = EXCLUDED.endpoint, peer_key = EXCLUDED.peer_key,
				position = EXCLUDED.position, active = true, updated_at = now()`,
			ids, names, countries, codes, cities, virtual, endpoints, peers, positions)
		if err != nil {
			return err
		}
	}
	// WireGuard configs come after the Surfshark lanes.
	if _, err := tx.Exec(ctx, `
		INSERT INTO lanes (id, provider, name, country_code, city, endpoint, peer_key, position, enabled, active)
		SELECT 'wireguard:' || id, 'wireguard', name, upper(country_code), city, endpoint, peer_key, 100000 + id, enabled, true
		FROM wireguard_configs
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, country_code = EXCLUDED.country_code, city = EXCLUDED.city,
			endpoint = EXCLUDED.endpoint, peer_key = EXCLUDED.peer_key, enabled = EXCLUDED.enabled, active = true, updated_at = now()`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE lanes SET active = false WHERE provider = 'wireguard'
		AND id NOT IN (SELECT 'wireguard:' || id FROM wireguard_configs)`); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.configChanged()
	return fetchErr
}

func (s *Server) laneSyncLoop(ctx context.Context) {
	t := time.NewTicker(6 * time.Hour) // pick up Surfshark server changes
	defer t.Stop()
	for {
		if err := s.syncLanes(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("lane sync failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.syncRequired:
		}
	}
}

// surfsharkServers returns the cached server list, refreshing it when older
// than an hour (or when force is set).
func (s *Server) surfsharkServers(ctx context.Context, force bool) ([]surfshark.Server, error) {
	s.serversMu.Lock()
	defer s.serversMu.Unlock()
	if !force && len(s.servers) > 0 && time.Since(s.serversAt) < time.Hour {
		return s.servers, nil
	}
	servers, err := surfshark.Fetch(ctx, s.cfg.SurfsharkAPI)
	if err != nil {
		s.serversErr = err.Error()
		return s.servers, fmt.Errorf("fetch Surfshark servers: %w", err)
	}
	s.servers, s.serversAt, s.serversErr = servers, time.Now(), ""
	return servers, nil
}

// poolIPs returns the known server IPs of the locations, looking them up when
// the cache is older than six hours. Results are merged with earlier lookups,
// since DNS only shows a few servers of a location at a time.
func (s *Server) poolIPs(ctx context.Context, locations []surfshark.Server) map[string][]netip.Addr {
	s.serverIPsMu.Lock()
	defer s.serverIPsMu.Unlock()
	hosts := make([]string, 0, len(locations))
	missing := false
	for _, l := range locations {
		hosts = append(hosts, l.ConnectionName)
		if _, ok := s.serverIPs[l.ConnectionName]; !ok {
			missing = true
		}
	}
	if !missing && time.Since(s.serverIPsAt) < 6*time.Hour {
		return s.serverIPs
	}
	found := surfshark.DiscoverIPs(ctx, hosts, 12, s.lookup)
	merged := make(map[string][]netip.Addr, len(found))
	total := 0
	for _, h := range hosts {
		set := map[netip.Addr]bool{}
		for _, a := range s.serverIPs[h] {
			set[a] = true
		}
		for _, a := range found[h] {
			set[a] = true
		}
		for a := range set {
			merged[h] = append(merged[h], a)
		}
		slices.SortFunc(merged[h], func(a, b netip.Addr) int { return a.Compare(b) })
		total += len(merged[h])
	}
	s.serverIPs, s.serverIPsAt = merged, time.Now()
	s.log.Info("discovered Surfshark servers", "locations", len(hosts), "servers", total)
	return merged
}
