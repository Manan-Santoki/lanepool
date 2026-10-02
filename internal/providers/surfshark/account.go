package surfshark

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/curve25519"
)

// Account talks to Surfshark's account API (the one Surfshark's own apps use) to
// manage the WireGuard keys registered on an account. It isn't a documented
// public API; the calls below are the ones open-source tools rely on.
type Account struct {
	BaseURL  string // default https://api.surfshark.com
	Email    string
	Password string
	HTTP     *http.Client

	mu    sync.Mutex
	token string
}

// RemoteKey is a public key registered on the account.
type RemoteKey struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	PubKey    string     `json:"pubKey"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
}

// ErrAuth means Surfshark rejected the email or password.
var ErrAuth = errors.New("Surfshark rejected the email or password")

func (a *Account) base() string {
	if a.BaseURL == "" {
		return "https://api.surfshark.com"
	}
	return strings.TrimRight(a.BaseURL, "/")
}

func (a *Account) client() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

// Login gets a fresh token.
func (a *Account) Login(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{"username": a.Email, "password": a.Password})
	var out struct {
		Token      string `json:"token"`
		RenewToken string `json:"renewToken"`
	}
	status, raw, err := a.send(ctx, http.MethodPost, "/v1/auth/login", "", body)
	if err != nil {
		return err
	}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Errorf("%w (HTTP %d%s)", ErrAuth, status, apiMessage(raw))
	case status >= 300:
		return fmt.Errorf("Surfshark login failed: HTTP %d%s", status, apiMessage(raw))
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Token == "" {
		return fmt.Errorf("Surfshark login: unexpected response%s", apiMessage(raw))
	}
	a.mu.Lock()
	a.token = out.Token
	a.mu.Unlock()
	return nil
}

func apiMessage(raw []byte) string {
	var e struct {
		Message string `json:"message"`
		Error   string `json:"error"`
		Detail  string `json:"detail"`
	}
	if json.Unmarshal(raw, &e) == nil {
		for _, m := range []string{e.Message, e.Error, e.Detail} {
			if m != "" {
				return ": " + m
			}
		}
	}
	return ""
}

func (a *Account) send(ctx context.Context, method, path, token string, body []byte) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base()+path, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "lanepool")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := a.client().Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("Surfshark API unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, raw, nil
}

// call sends an authenticated request, logging in first or again when the
// token is missing or expired.
func (a *Account) call(ctx context.Context, method, path string, in, out any) error {
	var body []byte
	if in != nil {
		body, _ = json.Marshal(in)
	}
	for attempt := 0; attempt < 2; attempt++ {
		a.mu.Lock()
		token := a.token
		a.mu.Unlock()
		if token == "" {
			if err := a.Login(ctx); err != nil {
				return err
			}
			continue
		}
		status, raw, err := a.send(ctx, method, path, token, body)
		if err != nil {
			return err
		}
		if status == http.StatusUnauthorized && attempt == 0 {
			a.mu.Lock()
			a.token = ""
			a.mu.Unlock()
			continue
		}
		if status >= 300 {
			return fmt.Errorf("Surfshark API %s %s: HTTP %d%s", method, path, status, apiMessage(raw))
		}
		if out != nil && len(raw) > 0 {
			if err := json.Unmarshal(raw, out); err != nil {
				return fmt.Errorf("Surfshark API %s: unexpected response", path)
			}
		}
		return nil
	}
	return ErrAuth
}

// Keys lists the public keys registered on the account.
func (a *Account) Keys(ctx context.Context) ([]RemoteKey, error) {
	var keys []RemoteKey
	err := a.call(ctx, http.MethodGet, "/v1/account/users/public-keys", nil, &keys)
	return keys, err
}

// Register registers a public key under name.
func (a *Account) Register(ctx context.Context, pubKey, name string) (RemoteKey, error) {
	var k RemoteKey
	err := a.call(ctx, http.MethodPost, "/v1/account/users/public-keys",
		map[string]any{"pubKey": pubKey, "name": name, "manual": true}, &k)
	if k.PubKey == "" {
		k.PubKey = pubKey
	}
	return k, err
}

// Validate extends a key's validity and returns its new expiry.
func (a *Account) Validate(ctx context.Context, pubKey string) (*time.Time, error) {
	var k RemoteKey
	err := a.call(ctx, http.MethodPost, "/v1/account/users/public-keys/validate", map[string]string{"pubKey": pubKey}, &k)
	return k.ExpiresAt, err
}

// Delete removes a key from the account. Every session that uses it ends.
func (a *Account) Delete(ctx context.Context, id string) error {
	return a.call(ctx, http.MethodDelete, "/v1/account/users/public-keys/"+id, nil, nil)
}

// NewKeyPair returns a fresh base64 WireGuard private and public key.
func NewKeyPair() (priv, pub string, err error) {
	var k [32]byte
	if _, err := rand.Read(k[:]); err != nil {
		return "", "", err
	}
	k[0] &= 248
	k[31] = (k[31] & 127) | 64
	p, err := curve25519.X25519(k[:], curve25519.Basepoint)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(k[:]), base64.StdEncoding.EncodeToString(p), nil
}
