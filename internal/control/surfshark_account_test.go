package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Manan-Santoki/lanepool/internal/providers/surfshark"
)

// fakeSurfshark imitates the account API: login, list/register/delete/validate keys.
type fakeSurfshark struct {
	mu     sync.Mutex
	keys   map[string]surfshark.RemoteKey
	nextID int
	logins int
}

func (f *fakeSurfshark) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/v1/auth/login" {
		var in struct{ Username, Password string }
		json.NewDecoder(r.Body).Decode(&in)
		if in.Password != "right-password" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"message":"bad credentials"}`))
			return
		}
		f.logins++
		json.NewEncoder(w).Encode(map[string]string{"token": "tok", "renewToken": "rt"})
		return
	}
	if r.Header.Get("Authorization") != "Bearer tok" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	exp := time.Now().Add(7 * 24 * time.Hour)
	switch {
	case r.URL.Path == "/v1/account/users/public-keys" && r.Method == http.MethodGet:
		out := []surfshark.RemoteKey{}
		for _, k := range f.keys {
			out = append(out, k)
		}
		json.NewEncoder(w).Encode(out)
	case r.URL.Path == "/v1/account/users/public-keys" && r.Method == http.MethodPost:
		var in struct{ PubKey, Name string }
		json.NewDecoder(r.Body).Decode(&in)
		f.nextID++
		k := surfshark.RemoteKey{ID: fmt.Sprintf("r%d", f.nextID), Name: in.Name, PubKey: in.PubKey, ExpiresAt: &exp}
		f.keys[k.ID] = k
		json.NewEncoder(w).Encode(k)
	case r.URL.Path == "/v1/account/users/public-keys/validate":
		json.NewEncoder(w).Encode(map[string]any{"expiresAt": exp})
	case strings.HasPrefix(r.URL.Path, "/v1/account/users/public-keys/") && r.Method == http.MethodDelete:
		id := strings.TrimPrefix(r.URL.Path, "/v1/account/users/public-keys/")
		if _, ok := f.keys[id]; !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		delete(f.keys, id)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeSurfshark) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.keys)
}

func TestSurfsharkAccountKeyManagement(t *testing.T) {
	fake := &fakeSurfshark{keys: map[string]surfshark.RemoteKey{}}
	api := httptest.NewServer(fake)
	defer api.Close()
	var servers []surfshark.Server
	for i := 0; i < 12; i++ {
		servers = append(servers, surfshark.Server{Country: "DE", CountryCode: "DE", Location: fmt.Sprint("City", i),
			ConnectionName: fmt.Sprintf("de-c%02d.prod.surfshark.com", i), PubKey: "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk="})
	}
	serverList := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(servers) }))
	defer serverList.Close()
	st := newStack(t, serverList.URL)
	st.srv.cfg.SurfsharkAccountAPI = api.URL
	st.call(st.client, "POST", "/api/setup", map[string]string{"email": "o@example.com", "password": "a-long-password"}, nil)

	// Not connected yet.
	if code := st.call(st.client, "POST", "/api/providers/surfshark/keys/generate", map[string]int{"count": 1}, nil); code != 400 {
		t.Fatalf("generate without account: %d", code)
	}
	var verr struct{ Fields map[string]string }
	if code := st.call(st.client, "PUT", "/api/providers/surfshark/account", map[string]string{"email": "me@example.com", "password": "nope"}, &verr); code != 422 || verr.Fields["password"] == "" {
		t.Fatalf("wrong password: %d %+v", code, verr)
	}
	var acct map[string]any
	if code := st.call(st.client, "PUT", "/api/providers/surfshark/account", map[string]string{"email": "me@example.com", "password": "right-password"}, &acct); code != 200 || acct["connected"] != true {
		t.Fatalf("connect: %d %v", code, acct)
	}
	// The password is stored encrypted.
	var raw string
	st.srv.db.QueryRow(context.Background(), `SELECT value::text FROM settings WHERE key = 'surfshark.account'`).Scan(&raw)
	if strings.Contains(raw, "right-password") {
		t.Fatal("Surfshark password stored in plain text")
	}

	// Generate keys: registered at Surfshark and stored as managed keys.
	var keys []surfsharkKey
	if code := st.call(st.client, "POST", "/api/providers/surfshark/keys/generate", map[string]int{"count": 3}, &keys); code != 200 || len(keys) != 3 {
		t.Fatalf("generate: %d %d", code, len(keys))
	}
	if fake.count() != 3 {
		t.Fatalf("remote keys: %d", fake.count())
	}
	var prov struct{ Keys []surfsharkKey }
	st.call(st.client, "GET", "/api/providers/surfshark", nil, &prov)
	if len(prov.Keys) != 3 || !prov.Keys[0].Managed || prov.Keys[0].ExpiresAt == nil {
		t.Fatalf("local keys %+v", prov.Keys)
	}

	// Rotate: a new key replaces the old one at Surfshark and in lanepool.
	var rotated surfsharkKey
	if code := st.call(st.client, "POST", fmt.Sprintf("/api/providers/surfshark/keys/%d/rotate", keys[0].ID), nil, &rotated); code != 200 {
		t.Fatalf("rotate: %d", code)
	}
	st.call(st.client, "GET", "/api/providers/surfshark", nil, &prov)
	if fake.count() != 3 || len(prov.Keys) != 3 || prov.Keys[len(prov.Keys)-1].ID != rotated.ID {
		t.Fatalf("after rotate: remote %d local %+v", fake.count(), prov.Keys)
	}
	for _, k := range prov.Keys {
		if k.ID == keys[0].ID {
			t.Fatal("rotated key still in lanepool")
		}
	}

	// Delete: removed at Surfshark too.
	if code := st.call(st.client, "DELETE", fmt.Sprintf("/api/providers/surfshark/keys/%d", keys[1].ID), nil, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if fake.count() != 2 {
		t.Fatalf("remote after delete: %d", fake.count())
	}

	// A key that exists only at Surfshark shows up and can be deleted.
	_, strayPub, _ := surfshark.NewKeyPair()
	fake.mu.Lock()
	fake.keys["stray"] = surfshark.RemoteKey{ID: "stray", Name: "phone", PubKey: strayPub}
	fake.mu.Unlock()
	var view struct {
		RemoteKeys []remoteKeyView
	}
	st.call(st.client, "GET", "/api/providers/surfshark/account", nil, &view)
	var stray *remoteKeyView
	for i := range view.RemoteKeys {
		if view.RemoteKeys[i].ID == "stray" {
			stray = &view.RemoteKeys[i]
		}
	}
	if len(view.RemoteKeys) != 3 || stray == nil || stray.LocalKeyID != nil {
		t.Fatalf("remote view %+v", view.RemoteKeys)
	}
	if code := st.call(st.client, "DELETE", "/api/providers/surfshark/remote-keys/stray", nil, nil); code != 204 || fake.count() != 2 {
		t.Fatalf("delete stray: %d remote %d", code, fake.count())
	}

	// Automatic management tops up keys for the lanes: 12 lanes at 4 per key.
	if code := st.call(st.client, "PUT", "/api/providers/surfshark/selection", map[string]any{"lanes": 12, "includeVirtual": true}, nil); code != 200 {
		t.Fatalf("selection: %d", code)
	}
	if code := st.call(st.client, "PATCH", "/api/providers/surfshark/account", map[string]any{"autoManage": true, "lanesPerKey": 4}, nil); code != 200 {
		t.Fatalf("enable auto: %d", code)
	}
	st.srv.manageKeys(context.Background())
	if fake.count() != 3 { // 12 lanes / 4 per key = 3 keys
		t.Fatalf("auto top-up: remote %d", fake.count())
	}

	// Disconnecting keeps the keys.
	if code := st.call(st.client, "DELETE", "/api/providers/surfshark/account", nil, nil); code != 204 {
		t.Fatalf("disconnect: %d", code)
	}
	st.call(st.client, "GET", "/api/providers/surfshark", nil, &prov)
	if len(prov.Keys) != 3 {
		t.Fatalf("keys after disconnect: %d", len(prov.Keys))
	}
}
