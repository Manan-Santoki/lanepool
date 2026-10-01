package control

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/curve25519"

	"github.com/Manan-Santoki/lanepool/internal/protocol"
	"github.com/Manan-Santoki/lanepool/internal/wg"
)

// publicKey derives the WireGuard public key of a base64 private key.
func publicKey(priv string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(priv))
	if err != nil || len(raw) != 32 {
		return "", fmt.Errorf("not a WireGuard private key")
	}
	pub, err := curve25519.X25519(raw, curve25519.Basepoint)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(pub), nil
}

// --- Surfshark ------------------------------------------------------------------

type surfsharkKey struct {
	ID        int64     `json:"id"`
	Label     string    `json:"label"`
	PublicKey string    `json:"publicKey"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
	Lanes     int       `json:"lanes"`
	UpLanes   int       `json:"upLanes"`
}

func (s *Server) surfsharkKeys(ctx context.Context) ([]surfsharkKey, error) {
	rows, err := s.db.Query(ctx, `SELECT id, label, public_key, enabled, created_at FROM provider_keys
		WHERE provider = 'surfshark' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	keys, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (surfsharkKey, error) {
		var k surfsharkKey
		err := row.Scan(&k.ID, &k.Label, &k.PublicKey, &k.Enabled, &k.CreatedAt)
		return k, err
	})
	if err != nil {
		return nil, err
	}
	states, _ := s.latestLaneStates()
	for i := range keys {
		for _, st := range states {
			if st.KeyID == keys[i].ID {
				keys[i].Lanes++
				if st.Status == protocol.LaneUp {
					keys[i].UpLanes++
				}
			}
		}
	}
	return keys, nil
}

// enabledKeys returns decrypted keys for the engine.
func (s *Server) enabledKeys(ctx context.Context) ([]protocol.LaneKey, error) {
	rows, err := s.db.Query(ctx, `SELECT id, private_key_enc FROM provider_keys WHERE provider = 'surfshark' AND enabled ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocol.LaneKey
	for rows.Next() {
		var id int64
		var enc []byte
		if err := rows.Scan(&id, &enc); err != nil {
			return nil, err
		}
		plain, err := s.box.open(enc)
		if err != nil {
			return nil, fmt.Errorf("key %d: %w", id, err)
		}
		out = append(out, protocol.LaneKey{ID: id, PrivateKey: string(plain)})
	}
	return out, rows.Err()
}

func (s *Server) keyLabels(ctx context.Context) (map[int64]string, error) {
	rows, err := s.db.Query(ctx, `SELECT id, CASE WHEN label = '' THEN 'key ' || id ELSE label END FROM provider_keys`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var label string
		if err := rows.Scan(&id, &label); err != nil {
			return nil, err
		}
		out[id] = label
	}
	return out, rows.Err()
}

func (s *Server) getSurfshark(_ http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	keys, err := s.surfsharkKeys(ctx)
	if err != nil {
		return nil, err
	}
	sel, err := s.selection(ctx)
	if err != nil {
		return nil, err
	}
	s.serversMu.Lock()
	count, at, ferr := len(s.servers), s.serversAt, s.serversErr
	s.serversMu.Unlock()
	resp := map[string]any{"keys": keys, "selection": sel, "serverCount": count}
	if !at.IsZero() {
		resp["lastFetchedAt"] = at
	}
	if ferr != "" {
		resp["fetchError"] = ferr
	}
	return resp, nil
}

func (s *Server) addSurfsharkKey(_ http.ResponseWriter, r *http.Request) (any, error) {
	var in struct {
		PrivateKey string `json:"privateKey"`
		Label      string `json:"label"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	in.PrivateKey = strings.TrimSpace(in.PrivateKey)
	pub, err := publicKey(in.PrivateKey)
	if err != nil {
		return nil, errFields(map[string]string{"privateKey": "paste the private key from Surfshark (44 characters ending in =)"})
	}
	ctx := r.Context()
	k, err := s.insertKey(ctx, in.PrivateKey, pub, strings.TrimSpace(in.Label))
	if isUniqueViolation(err) {
		return nil, errFields(map[string]string{"privateKey": "this key is already added"})
	}
	if err != nil {
		return nil, err
	}
	s.audit(ctx, who(r), "admin.key_added", "added Surfshark key "+keyName(k.Label, k.ID))
	s.requestLaneSync()
	return k, nil
}

func (s *Server) insertKey(ctx context.Context, priv, pub, label string) (surfsharkKey, error) {
	var k surfsharkKey
	err := s.db.QueryRow(ctx, `INSERT INTO provider_keys (provider, label, private_key_enc, public_key)
		VALUES ('surfshark', $1, $2, $3) RETURNING id, label, public_key, enabled, created_at`,
		label, s.box.seal([]byte(priv)), pub).Scan(&k.ID, &k.Label, &k.PublicKey, &k.Enabled, &k.CreatedAt)
	return k, err
}

func keyName(label string, id int64) string {
	if label != "" {
		return label
	}
	return "#" + strconv.FormatInt(id, 10)
}

func (s *Server) patchSurfsharkKey(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := idParam(r)
	if err != nil {
		return nil, err
	}
	var in struct {
		Enabled *bool   `json:"enabled"`
		Label   *string `json:"label"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	var k surfsharkKey
	err = s.db.QueryRow(r.Context(), `UPDATE provider_keys SET enabled = COALESCE($2, enabled), label = COALESCE($3, label)
		WHERE id = $1 AND provider = 'surfshark' RETURNING id, label, public_key, enabled, created_at`, id, in.Enabled, in.Label).
		Scan(&k.ID, &k.Label, &k.PublicKey, &k.Enabled, &k.CreatedAt)
	if err != nil {
		return nil, err
	}
	s.audit(r.Context(), who(r), "admin.key_updated", "updated Surfshark key "+keyName(k.Label, k.ID))
	s.requestLaneSync()
	return k, nil
}

func (s *Server) deleteSurfsharkKey(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := idParam(r)
	if err != nil {
		return nil, err
	}
	var label string
	if err := s.db.QueryRow(r.Context(), `DELETE FROM provider_keys WHERE id = $1 AND provider = 'surfshark' RETURNING label`, id).Scan(&label); err != nil {
		return nil, err
	}
	s.audit(r.Context(), who(r), "admin.key_deleted", "deleted Surfshark key "+keyName(label, id))
	s.requestLaneSync()
	return nil, nil
}

func (s *Server) putSurfsharkSelection(w http.ResponseWriter, r *http.Request) (any, error) {
	var in SurfsharkSelection
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if in.Lanes < 0 || in.Lanes > 500 {
		return nil, errFields(map[string]string{"lanes": "between 0 and 500"})
	}
	norm := func(list []string) []string {
		out := []string{}
		for _, v := range list {
			if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
				out = append(out, v)
			}
		}
		return out
	}
	in.Countries, in.ExcludeCountries = norm(in.Countries), norm(in.ExcludeCountries)
	in.Locations, in.ExcludeLocations = norm(in.Locations), norm(in.ExcludeLocations)
	ctx := r.Context()
	if err := s.putSetting(ctx, "surfshark.selection", in); err != nil {
		return nil, err
	}
	if err := s.syncLanes(ctx); err != nil {
		s.log.Warn("lane sync after selection change", "err", err)
	}
	s.audit(ctx, who(r), "admin.selection", fmt.Sprintf("changed Surfshark lane selection (%d lanes)", in.Lanes))
	return s.getSurfshark(w, r)
}

type surfsharkLocation struct {
	ID          string `json:"id"`
	Country     string `json:"country"`
	CountryCode string `json:"countryCode"`
	City        string `json:"city"`
	Virtual     bool   `json:"virtual"`
	Load        int    `json:"load"`
}

func (s *Server) surfsharkLocations(_ http.ResponseWriter, r *http.Request) (any, error) {
	servers, err := s.surfsharkServers(r.Context(), false)
	if err != nil && len(servers) == 0 {
		return nil, errStatus(http.StatusBadGateway, err.Error())
	}
	out := make([]surfsharkLocation, 0, len(servers))
	for _, sv := range servers {
		out = append(out, surfsharkLocation{ID: sv.ID(), Country: sv.Country, CountryCode: strings.ToUpper(sv.CountryCode),
			City: sv.Location, Virtual: sv.Virtual(), Load: sv.Load})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// --- generic WireGuard configs ------------------------------------------------

type wireguardConfig struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	CountryCode string    `json:"countryCode"`
	City        string    `json:"city"`
	Endpoint    string    `json:"endpoint"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"createdAt"`
}

const wgCols = `id, name, country_code, city, endpoint, enabled, created_at`

func scanWG(row pgx.Row) (wireguardConfig, error) {
	var w wireguardConfig
	err := row.Scan(&w.ID, &w.Name, &w.CountryCode, &w.City, &w.Endpoint, &w.Enabled, &w.CreatedAt)
	return w, err
}

func (s *Server) listWireguard(_ http.ResponseWriter, r *http.Request) (any, error) {
	rows, err := s.db.Query(r.Context(), `SELECT `+wgCols+` FROM wireguard_configs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (wireguardConfig, error) { return scanWG(row) })
}

// parsedWG is a wg-quick config with one peer.
type parsedWG struct {
	PrivateKey, PeerKey, PresharedKey, Endpoint string
	Addresses, DNS                              []string
	MTU                                         int
}

// parseWGQuick reads a wg-quick style config. wg-quick-only keys (PostUp,
// Table, ...) are ignored; addresses are narrowed to single hosts.
func parseWGQuick(text string) (parsedWG, error) {
	var p parsedWG
	section := ""
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if i := strings.IndexAny(line, "#;"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.ToLower(strings.Trim(line, "[] "))
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		// Cut splits at the first "=", so base64 padding stays in the value.
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		list := func() []string {
			var out []string
			for _, x := range strings.Split(v, ",") {
				if x = strings.TrimSpace(x); x != "" {
					out = append(out, x)
				}
			}
			return out
		}
		switch section + "." + k {
		case "interface.privatekey":
			p.PrivateKey = v
		case "interface.address":
			for _, a := range list() {
				ip := strings.Split(a, "/")[0]
				if addr, err := netip.ParseAddr(ip); err == nil {
					p.Addresses = append(p.Addresses, addr.String())
				}
			}
		case "interface.dns":
			for _, d := range list() {
				if _, err := netip.ParseAddr(d); err == nil {
					p.DNS = append(p.DNS, d)
				}
			}
		case "interface.mtu":
			p.MTU, _ = strconv.Atoi(v)
		case "peer.publickey":
			p.PeerKey = v
		case "peer.presharedkey":
			p.PresharedKey = v
		case "peer.endpoint":
			p.Endpoint = v
		}
	}
	switch {
	case !wg.ValidKey(p.PrivateKey):
		return p, fmt.Errorf("missing or invalid [Interface] PrivateKey")
	case !wg.ValidKey(p.PeerKey):
		return p, fmt.Errorf("missing or invalid [Peer] PublicKey")
	case p.PresharedKey != "" && !wg.ValidKey(p.PresharedKey):
		return p, fmt.Errorf("invalid PresharedKey")
	case len(p.Addresses) == 0:
		return p, fmt.Errorf("missing [Interface] Address")
	}
	if _, _, err := net.SplitHostPort(p.Endpoint); err != nil {
		return p, fmt.Errorf("missing or invalid [Peer] Endpoint (host:port)")
	}
	return p, nil
}

func (s *Server) createWireguard(_ http.ResponseWriter, r *http.Request) (any, error) {
	var in struct {
		Name        string `json:"name"`
		Config      string `json:"config"`
		CountryCode string `json:"countryCode"`
		City        string `json:"city"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	f := map[string]string{}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 64 {
		f["name"] = "required (up to 64 characters)"
	}
	if in.CountryCode != "" && len(in.CountryCode) != 2 {
		f["countryCode"] = "2-letter country code"
	}
	p, err := parseWGQuick(in.Config)
	if err != nil {
		f["config"] = err.Error()
	}
	if len(f) > 0 {
		return nil, errFields(f)
	}
	var psk []byte
	if p.PresharedKey != "" {
		psk = s.box.seal([]byte(p.PresharedKey))
	}
	ctx := r.Context()
	w, err := scanWG(s.db.QueryRow(ctx, `INSERT INTO wireguard_configs
		(name, country_code, city, endpoint, peer_key, preshared_key_enc, private_key_enc, addresses, dns, mtu)
		VALUES ($1, upper($2), $3, $4, $5, $6, $7, $8, $9, $10) RETURNING `+wgCols,
		in.Name, in.CountryCode, strings.TrimSpace(in.City), p.Endpoint, p.PeerKey, psk, s.box.seal([]byte(p.PrivateKey)),
		p.Addresses, nonNil(p.DNS), p.MTU))
	if isUniqueViolation(err) {
		return nil, errFields(map[string]string{"name": "a config with this name exists"})
	}
	if err != nil {
		return nil, err
	}
	s.audit(ctx, who(r), "admin.wireguard_added", "added WireGuard config "+w.Name)
	s.requestLaneSync()
	return w, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (s *Server) patchWireguard(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := idParam(r)
	if err != nil {
		return nil, err
	}
	var in struct {
		Enabled     *bool   `json:"enabled"`
		Name        *string `json:"name"`
		CountryCode *string `json:"countryCode"`
		City        *string `json:"city"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	w, err := scanWG(s.db.QueryRow(r.Context(), `UPDATE wireguard_configs SET enabled = COALESCE($2, enabled),
		name = COALESCE($3, name), country_code = COALESCE(upper($4), country_code), city = COALESCE($5, city)
		WHERE id = $1 RETURNING `+wgCols, id, in.Enabled, in.Name, in.CountryCode, in.City))
	if err != nil {
		return nil, err
	}
	s.audit(r.Context(), who(r), "admin.wireguard_updated", "updated WireGuard config "+w.Name)
	s.requestLaneSync()
	return w, nil
}

func (s *Server) deleteWireguard(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := idParam(r)
	if err != nil {
		return nil, err
	}
	var name string
	if err := s.db.QueryRow(r.Context(), `DELETE FROM wireguard_configs WHERE id = $1 RETURNING name`, id).Scan(&name); err != nil {
		return nil, err
	}
	s.audit(r.Context(), who(r), "admin.wireguard_deleted", "deleted WireGuard config "+name)
	s.requestLaneSync()
	return nil, nil
}

type wgSpec struct {
	Endpoint, PeerKey, PresharedKey string
	Addresses, DNS                  []string
	MTU                             int
	Keys                            []protocol.LaneKey
}

func (s *Server) wireguardSpecs(ctx context.Context) (map[string]wgSpec, error) {
	rows, err := s.db.Query(ctx, `SELECT id, endpoint, peer_key, preshared_key_enc, private_key_enc, addresses, dns, mtu FROM wireguard_configs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]wgSpec{}
	for rows.Next() {
		var id int64
		var w wgSpec
		var psk, priv []byte
		if err := rows.Scan(&id, &w.Endpoint, &w.PeerKey, &psk, &priv, &w.Addresses, &w.DNS, &w.MTU); err != nil {
			return nil, err
		}
		key, err := s.box.open(priv)
		if err != nil {
			return nil, fmt.Errorf("wireguard config %d: %w", id, err)
		}
		if psk != nil {
			p, err := s.box.open(psk)
			if err != nil {
				return nil, err
			}
			w.PresharedKey = string(p)
		}
		// Keys need stable IDs that don't collide with provider keys.
		sum := sha256.Sum256(key)
		kid := -int64(uint64(sum[0])<<24|uint64(sum[1])<<16|uint64(sum[2])<<8|uint64(sum[3])) - 1
		w.Keys = []protocol.LaneKey{{ID: kid, PrivateKey: string(key)}}
		out["wireguard:"+strconv.FormatInt(id, 10)] = w
	}
	return out, rows.Err()
}
