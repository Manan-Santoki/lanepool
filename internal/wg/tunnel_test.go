package wg_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Manan-Santoki/lanepool/internal/wg"
	"github.com/Manan-Santoki/lanepool/internal/wg/wgtest"
)

func TestHandshakeAndHTTPThroughTunnel(t *testing.T) {
	priv, pub := wgtest.KeyPair()
	prov := wgtest.Start(t, "203.0.113.10", pub)

	tun, err := wg.Start(context.Background(), prov.ClientConfig(priv), t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Close()

	client := http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{DialContext: tun.DialContext},
	}
	// Resolved through the provider's DNS, inside the tunnel.
	resp, err := client.Get("http://api.ipify.org/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if got := strings.TrimSpace(string(body)); got != "203.0.113.10" {
		t.Fatalf("exit IP = %q", got)
	}
	if s := tun.Stats(); s.LastHandshake.IsZero() || s.RxBytes == 0 {
		t.Fatalf("stats not populated: %+v", s)
	}
}

func TestWrongKeyNeverHandshakes(t *testing.T) {
	priv, _ := wgtest.KeyPair()
	_, otherPub := wgtest.KeyPair()
	prov := wgtest.Start(t, "203.0.113.11", otherPub) // provider doesn't know our key

	tun, err := wg.Start(context.Background(), prov.ClientConfig(priv), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if c, err := tun.DialContext(ctx, "tcp", net.JoinHostPort(wgtest.WebAddr.String(), "80")); err == nil {
		c.Close()
		t.Fatal("dial succeeded with an unknown key")
	}
	if !tun.Stats().LastHandshake.IsZero() {
		t.Fatal("handshake completed with an unknown key")
	}
}

func TestValidKey(t *testing.T) {
	priv, pub := wgtest.KeyPair()
	if !wg.ValidKey(priv) || !wg.ValidKey(pub) {
		t.Fatal("generated keys rejected")
	}
	for _, bad := range []string{"", "abc", "6FbsbSjExReTziuvxwPvGIOCiFlhieDTFrNU9U6ES1"} {
		if wg.ValidKey(bad) {
			t.Fatalf("accepted %q", bad)
		}
	}
}
