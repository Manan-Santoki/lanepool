package control

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Manan-Santoki/lanepool/internal/protocol"
	"github.com/Manan-Santoki/lanepool/internal/providers/surfshark"
)

// accountSettings is stored in settings["surfshark.account"]. The password is
// sealed with LANEPOOL_SECRET.
type accountSettings struct {
	Email          string     `json:"email"`
	SealedPassword string     `json:"sealedPassword"`
	AutoManage     bool       `json:"autoManage"`
	LanesPerKey    int        `json:"lanesPerKey"`    // generate keys so each has at most this many lanes
	RotateFailures int        `json:"rotateFailures"` // rotate a key after this many of its lanes failed
	LastError      string     `json:"lastError"`
	LastSyncAt     *time.Time `json:"lastSyncAt"`
}

func defaultAccountSettings() accountSettings {
	return accountSettings{LanesPerKey: 5, RotateFailures: 2}
}

// surfsharkAccounts caches the API client so its token is reused.
type surfsharkAccounts struct {
	mu      sync.Mutex
	key     string
	account *surfshark.Account
}

func (s *Server) accountSettings(ctx context.Context) (accountSettings, error) {
	a := defaultAccountSettings()
	err := s.getSetting(ctx, "surfshark.account", &a)
	return a, err
}

// surfsharkAccount returns an API client for the connected account, or nil.
func (s *Server) surfsharkAccount(ctx context.Context) (*surfshark.Account, accountSettings, error) {
	st, err := s.accountSettings(ctx)
	if err != nil || st.Email == "" || st.SealedPassword == "" {
		return nil, st, err
	}
	sealed, err := base64.StdEncoding.DecodeString(st.SealedPassword)
	if err != nil {
		return nil, st, err
	}
	pw, err := s.box.open(sealed)
	if err != nil {
		return nil, st, err
	}
	s.accounts.mu.Lock()
	defer s.accounts.mu.Unlock()
	cacheKey := st.Email + "\x00" + st.SealedPassword
	if s.accounts.account == nil || s.accounts.key != cacheKey {
		s.accounts.account = &surfshark.Account{BaseURL: s.cfg.SurfsharkAccountAPI, UserAgent: s.cfg.SurfsharkUserAgent, Email: st.Email, Password: string(pw)}
		s.accounts.key = cacheKey
	}
	return s.accounts.account, st, nil
}

func (s *Server) requireAccount(ctx context.Context) (*surfshark.Account, error) {
	acct, _, err := s.surfsharkAccount(ctx)
	if err != nil {
		return nil, err
	}
	if acct == nil {
		return nil, errStatus(http.StatusBadRequest, "connect your Surfshark account first")
	}
	return acct, nil
}

func (s *Server) noteAccountResult(ctx context.Context, err error) {
	st, _ := s.accountSettings(ctx)
	now := time.Now()
	st.LastSyncAt = &now
	st.LastError = ""
	if err != nil {
		st.LastError = err.Error()
	}
	s.putSetting(ctx, "surfshark.account", st)
}

// remoteKeyView is a key registered at Surfshark, matched with lanepool's keys.
type remoteKeyView struct {
	surfshark.RemoteKey
	LocalKeyID *int64 `json:"localKeyId,omitempty"` // lanepool key with the same public key
}

func (s *Server) getSurfsharkAccount(_ http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	acct, st, err := s.surfsharkAccount(ctx)
	if err != nil {
		return nil, err
	}
	resp := map[string]any{
		"connected": acct != nil, "email": st.Email, "autoManage": st.AutoManage, "lanesPerKey": st.LanesPerKey,
		"rotateFailures": st.RotateFailures, "lastError": st.LastError, "lastSyncAt": st.LastSyncAt,
	}
	if acct == nil {
		return resp, nil
	}
	remote, err := acct.Keys(ctx)
	s.noteAccountResult(ctx, err)
	if err != nil {
		resp["lastError"] = err.Error()
		resp["remoteKeys"] = []remoteKeyView{}
		return resp, nil
	}
	resp["lastError"] = ""
	local := map[string]int64{}
	rows, err := s.db.Query(ctx, `SELECT id, public_key FROM provider_keys WHERE provider = 'surfshark'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var pub string
		if rows.Scan(&id, &pub) == nil {
			local[pub] = id
		}
	}
	rows.Close()
	views := make([]remoteKeyView, 0, len(remote))
	for _, k := range remote {
		v := remoteKeyView{RemoteKey: k}
		if id, ok := local[k.PubKey]; ok {
			v.LocalKeyID = &id
		}
		views = append(views, v)
	}
	resp["remoteKeys"] = views
	return resp, nil
}

func (s *Server) putSurfsharkAccount(w http.ResponseWriter, r *http.Request) (any, error) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	in.Email = strings.TrimSpace(in.Email)
	f := map[string]string{}
	if in.Email == "" {
		f["email"] = "required"
	}
	if in.Password == "" {
		f["password"] = "required"
	}
	if len(f) > 0 {
		return nil, errFields(f)
	}
	ctx := r.Context()
	// Check the login before storing anything.
	test := &surfshark.Account{BaseURL: s.cfg.SurfsharkAccountAPI, UserAgent: s.cfg.SurfsharkUserAgent, Email: in.Email, Password: in.Password}
	if err := test.Login(ctx); err != nil {
		if errors.Is(err, surfshark.ErrAuth) {
			return nil, errFields(map[string]string{"password": err.Error()})
		}
		return nil, errStatus(http.StatusFailedDependency, err.Error())
	}
	st, err := s.accountSettings(ctx)
	if err != nil {
		return nil, err
	}
	st.Email = in.Email
	st.SealedPassword = base64.StdEncoding.EncodeToString(s.box.seal([]byte(in.Password)))
	st.LastError = ""
	if err := s.putSetting(ctx, "surfshark.account", st); err != nil {
		return nil, err
	}
	s.audit(ctx, who(r), "admin.surfshark_account", "connected Surfshark account "+in.Email)
	return s.getSurfsharkAccount(w, r)
}

func (s *Server) patchSurfsharkAccount(w http.ResponseWriter, r *http.Request) (any, error) {
	var in struct {
		AutoManage     *bool `json:"autoManage"`
		LanesPerKey    *int  `json:"lanesPerKey"`
		RotateFailures *int  `json:"rotateFailures"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	st, err := s.accountSettings(ctx)
	if err != nil {
		return nil, err
	}
	if in.AutoManage != nil {
		st.AutoManage = *in.AutoManage
	}
	if in.LanesPerKey != nil {
		if *in.LanesPerKey < 1 || *in.LanesPerKey > 100 {
			return nil, errFields(map[string]string{"lanesPerKey": "between 1 and 100"})
		}
		st.LanesPerKey = *in.LanesPerKey
	}
	if in.RotateFailures != nil {
		if *in.RotateFailures < 1 || *in.RotateFailures > 50 {
			return nil, errFields(map[string]string{"rotateFailures": "between 1 and 50"})
		}
		st.RotateFailures = *in.RotateFailures
	}
	if err := s.putSetting(ctx, "surfshark.account", st); err != nil {
		return nil, err
	}
	s.audit(ctx, who(r), "admin.surfshark_account", fmt.Sprintf("updated Surfshark key management (automatic: %v)", st.AutoManage))
	return s.getSurfsharkAccount(w, r)
}

func (s *Server) deleteSurfsharkAccount(_ http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	st, err := s.accountSettings(ctx)
	if err != nil {
		return nil, err
	}
	email := st.Email
	st.Email, st.SealedPassword, st.AutoManage, st.LastError = "", "", false, ""
	if err := s.putSetting(ctx, "surfshark.account", st); err != nil {
		return nil, err
	}
	s.accounts.mu.Lock()
	s.accounts.account = nil
	s.accounts.mu.Unlock()
	s.audit(ctx, who(r), "admin.surfshark_account", "disconnected Surfshark account "+email+" (keys were kept)")
	return nil, nil
}

// --- key operations ---------------------------------------------------------------

// generateKeys creates n key pairs, registers them at Surfshark and stores them.
func (s *Server) generateKeys(ctx context.Context, acct *surfshark.Account, n int, reason string) ([]surfsharkKey, error) {
	var out []surfsharkKey
	for i := 0; i < n; i++ {
		priv, pub, err := surfshark.NewKeyPair()
		if err != nil {
			return out, err
		}
		label := fmt.Sprintf("lanepool %s", time.Now().UTC().Format("0102-150405"))
		if n > 1 {
			label += fmt.Sprintf("-%d", i+1)
		}
		rk, err := acct.Register(ctx, pub, label)
		if err != nil {
			s.noteAccountResult(ctx, err)
			return out, errStatus(http.StatusFailedDependency, err.Error())
		}
		k, err := s.insertKey(ctx, priv, pub, label)
		if err != nil {
			acct.Delete(ctx, rk.ID) // don't leave an orphan at Surfshark
			return out, err
		}
		if _, err := s.db.Exec(ctx, `UPDATE provider_keys SET managed = true, remote_id = $2, expires_at = $3 WHERE id = $1`,
			k.ID, rk.ID, rk.ExpiresAt); err != nil {
			return out, err
		}
		out = append(out, k)
	}
	if len(out) > 0 {
		s.addEvent(ctx, "info", "surfshark_keys_generated", "", "surfshark", fmt.Sprintf("generated %d key(s): %s", len(out), reason))
		s.requestLaneSync()
	}
	return out, nil
}

// remoteIDFor finds a key's ID at Surfshark (stored, or looked up by public key).
func (s *Server) remoteIDFor(ctx context.Context, acct *surfshark.Account, keyID int64) (string, string, error) {
	var remote *string
	var pub string
	if err := s.db.QueryRow(ctx, `SELECT remote_id, public_key FROM provider_keys WHERE id = $1 AND provider = 'surfshark'`, keyID).Scan(&remote, &pub); err != nil {
		return "", "", err
	}
	if remote != nil && *remote != "" {
		return *remote, pub, nil
	}
	keys, err := acct.Keys(ctx)
	if err != nil {
		return "", pub, err
	}
	for _, k := range keys {
		if k.PubKey == pub {
			return k.ID, pub, nil
		}
	}
	return "", pub, nil // not registered at Surfshark (any more)
}

// deleteKeyEverywhere deletes a key at Surfshark (ending its sessions) and in lanepool.
func (s *Server) deleteKeyEverywhere(ctx context.Context, acct *surfshark.Account, keyID int64) (string, error) {
	if acct != nil {
		remoteID, _, err := s.remoteIDFor(ctx, acct, keyID)
		if err != nil {
			return "", err
		}
		if remoteID != "" {
			if err := acct.Delete(ctx, remoteID); err != nil && !strings.Contains(err.Error(), "HTTP 404") {
				s.noteAccountResult(ctx, err)
				return "", errStatus(http.StatusFailedDependency, "deleting at Surfshark: "+err.Error())
			}
		}
	}
	var label string
	err := s.db.QueryRow(ctx, `DELETE FROM provider_keys WHERE id = $1 AND provider = 'surfshark' RETURNING label`, keyID).Scan(&label)
	return label, err
}

func (s *Server) generateSurfsharkKeys(_ http.ResponseWriter, r *http.Request) (any, error) {
	var in struct {
		Count int `json:"count"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if in.Count < 1 || in.Count > 20 {
		return nil, errFields(map[string]string{"count": "between 1 and 20"})
	}
	ctx := r.Context()
	acct, err := s.requireAccount(ctx)
	if err != nil {
		return nil, err
	}
	keys, err := s.generateKeys(ctx, acct, in.Count, "requested by "+who(r).name())
	if err != nil {
		return nil, err
	}
	s.audit(ctx, who(r), "admin.keys_generated", fmt.Sprintf("generated %d Surfshark key(s)", len(keys)))
	return keys, nil
}

// rotateKey replaces a key with a fresh one and deletes the old one at
// Surfshark, so its sessions end immediately. Lanes on it reconnect.
func (s *Server) rotateKey(ctx context.Context, acct *surfshark.Account, keyID int64, reason string) (surfsharkKey, error) {
	keys, err := s.generateKeys(ctx, acct, 1, reason)
	if err != nil {
		return surfsharkKey{}, err
	}
	if _, err := s.deleteKeyEverywhere(ctx, acct, keyID); err != nil {
		return keys[0], err
	}
	s.db.Exec(ctx, `UPDATE provider_keys SET rotated_at = now() WHERE id = $1`, keys[0].ID)
	s.addEvent(ctx, "info", "surfshark_key_rotated", "", "surfshark", fmt.Sprintf("key %d replaced by key %d: %s", keyID, keys[0].ID, reason))
	s.requestLaneSync()
	return keys[0], nil
}

func (s *Server) rotateSurfsharkKey(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := idParam(r)
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	acct, err := s.requireAccount(ctx)
	if err != nil {
		return nil, err
	}
	k, err := s.rotateKey(ctx, acct, id, "rotated by "+who(r).name())
	if err != nil {
		return nil, err
	}
	s.audit(ctx, who(r), "admin.key_rotated", fmt.Sprintf("rotated Surfshark key %d (new key %d)", id, k.ID))
	return k, nil
}

func (s *Server) deleteRemoteKey(_ http.ResponseWriter, r *http.Request) (any, error) {
	remoteID := chi.URLParam(r, "remoteId")
	ctx := r.Context()
	acct, err := s.requireAccount(ctx)
	if err != nil {
		return nil, err
	}
	keys, err := acct.Keys(ctx)
	if err != nil {
		return nil, errStatus(http.StatusFailedDependency, err.Error())
	}
	var pub string
	for _, k := range keys {
		if k.ID == remoteID {
			pub = k.PubKey
		}
	}
	if pub == "" {
		return nil, errNotFound
	}
	if err := acct.Delete(ctx, remoteID); err != nil {
		return nil, errStatus(http.StatusFailedDependency, err.Error())
	}
	// A lanepool key with the same public key can't connect any more.
	s.db.Exec(ctx, `DELETE FROM provider_keys WHERE provider = 'surfshark' AND public_key = $1`, pub)
	s.audit(ctx, who(r), "admin.remote_key_deleted", "deleted key "+remoteID+" at Surfshark")
	s.requestLaneSync()
	return nil, nil
}

// --- automatic management ---------------------------------------------------------

// manageKeys runs periodically: renew expiring keys, generate keys as lanes
// grow, and rotate keys whose lanes keep failing.
func (s *Server) manageKeys(ctx context.Context) {
	acct, st, err := s.surfsharkAccount(ctx)
	if err != nil || acct == nil || !st.AutoManage {
		return
	}
	var runErr error
	defer func() { s.noteAccountResult(ctx, runErr) }()

	// 1. Renew managed keys that expire within 3 days.
	rows, err := s.db.Query(ctx, `SELECT id, public_key FROM provider_keys
		WHERE provider = 'surfshark' AND managed AND expires_at IS NOT NULL AND expires_at < now() + interval '3 days'`)
	if err != nil {
		runErr = err
		return
	}
	type keyRef struct {
		id  int64
		pub string
	}
	var expiring []keyRef
	for rows.Next() {
		var k keyRef
		if rows.Scan(&k.id, &k.pub) == nil {
			expiring = append(expiring, k)
		}
	}
	rows.Close()
	for _, k := range expiring {
		exp, err := acct.Validate(ctx, k.pub)
		if err != nil {
			runErr = err
			continue
		}
		s.db.Exec(ctx, `UPDATE provider_keys SET expires_at = $2 WHERE id = $1`, k.id, exp)
	}

	// 2. Enough keys for the lanes.
	var lanes, keys int
	s.db.QueryRow(ctx, `SELECT count(*) FROM lanes WHERE provider = 'surfshark' AND active AND enabled`).Scan(&lanes)
	s.db.QueryRow(ctx, `SELECT count(*) FROM provider_keys WHERE provider = 'surfshark' AND enabled`).Scan(&keys)
	if need := (lanes + st.LanesPerKey - 1) / st.LanesPerKey; keys < need {
		if _, err := s.generateKeys(ctx, acct, min(need-keys, 5), fmt.Sprintf("%d lanes need %d keys at %d lanes per key", lanes, need, st.LanesPerKey)); err != nil {
			runErr = err
		}
	}

	// 3. Rotate keys whose lanes keep failing.
	for _, keyID := range s.failingKeys(ctx, st.RotateFailures) {
		if _, err := s.rotateKey(ctx, acct, keyID, "its lanes kept failing to connect"); err != nil {
			runErr = err
		}
	}
}

// failingKeys returns keys (at most two per run) that recently failed on at
// least threshold lanes while no lane is up on them, and that weren't created
// or rotated in the last 30 minutes.
func (s *Server) failingKeys(ctx context.Context, threshold int) []int64 {
	s.mu.RLock()
	rep := s.report
	s.mu.RUnlock()
	if rep == nil {
		return nil
	}
	specs, err := s.laneSpecs(ctx)
	if err != nil {
		return nil
	}
	order := map[string][]int64{}
	for _, sp := range specs {
		for _, k := range sp.Keys {
			order[sp.ID] = append(order[sp.ID], k.ID)
		}
	}
	failed, up := map[int64]int{}, map[int64]bool{}
	for _, l := range rep.Lanes {
		switch l.Status {
		case protocol.LaneUp:
			up[l.KeyID] = true
		case protocol.LaneBackoff:
			// The engine moved the lane to the next key; the one that failed is the previous one.
			keys := order[l.ID]
			for i, k := range keys {
				if k == l.KeyID && len(keys) > 1 {
					failed[keys[(i-1+len(keys))%len(keys)]]++
				}
			}
		}
	}
	var out []int64
	for k, n := range failed {
		if n < threshold || up[k] || len(out) >= 2 {
			continue
		}
		var fresh bool
		s.db.QueryRow(ctx, `SELECT created_at > now() - interval '30 minutes'
			OR COALESCE(rotated_at > now() - interval '30 minutes', false)
			FROM provider_keys WHERE id = $1`, k).Scan(&fresh)
		if !fresh {
			out = append(out, k)
		}
	}
	return out
}
