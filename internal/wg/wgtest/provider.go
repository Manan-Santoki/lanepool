// Package wgtest runs a fake VPN provider in-process for tests: a real WireGuard
// server peer on localhost UDP whose userspace network answers HTTP and DNS like
// the internet would, reporting a configurable "exit IP".
package wgtest

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"testing"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/net/dns/dnsmessage"

	"github.com/Manan-Santoki/lanepool/internal/wg"
)

// Addresses inside the fake provider's network.
var (
	ServerAddr = netip.MustParseAddr("10.14.0.1")
	ClientAddr = netip.MustParseAddr("10.14.0.2")
	WebAddr    = netip.MustParseAddr("198.51.100.7")  // answers HTTP on :80 and echoes on :7
	DNSAddr    = netip.MustParseAddr("198.51.100.53") // answers every A query with WebAddr
)

// KeyPair returns a new base64 private and public key.
func KeyPair() (priv, pub string) {
	var k [32]byte
	if _, err := rand.Read(k[:]); err != nil {
		panic(err)
	}
	k[0] &= 248
	k[31] = (k[31] & 127) | 64
	p, err := curve25519.X25519(k[:], curve25519.Basepoint)
	if err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(k[:]), base64.StdEncoding.EncodeToString(p)
}

// Provider is one fake VPN server location.
type Provider struct {
	ExitIP    string // what the HTTP server reports as the client's public IP
	PublicKey string // server public key
	Endpoint  string // 127.0.0.1:port

	tun       *wg.Tunnel
	clientKey string
}

// Start runs a provider that accepts only clientPub. The returned provider is
// closed when the test ends.
func Start(t testing.TB, exitIP, clientPub string) *Provider {
	t.Helper()
	p, err := Run(exitIP, clientPub, 0)
	if err != nil {
		t.Fatalf("start provider: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// Run starts a provider outside tests (see cmd/fakeprovider). port 0 picks a
// free UDP port.
func Run(exitIP, clientPub string, port int) (*Provider, error) {
	priv, pub := KeyPair()
	if port == 0 {
		var err error
		if port, err = freeUDPPort(); err != nil {
			return nil, err
		}
	}
	tun, err := wg.Start(context.Background(), wg.Config{
		PrivateKey: priv,
		Addresses:  []netip.Addr{ServerAddr, WebAddr, DNSAddr},
		PeerKey:    clientPub,
		AllowedIPs: []netip.Prefix{netip.PrefixFrom(ClientAddr, 32)},
		Keepalive:  -1,
		ListenPort: port,
	}, nil)
	if err != nil {
		return nil, err
	}
	p := &Provider{ExitIP: exitIP, PublicKey: pub, Endpoint: fmt.Sprintf("127.0.0.1:%d", port), tun: tun, clientKey: clientPub}
	web, err := tun.Listen(80)
	if err != nil {
		tun.Close()
		return nil, err
	}
	go http.Serve(web, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bytes" {
			w.Write(make([]byte, 64*1024))
			return
		}
		io.WriteString(w, p.ExitIP)
	}))
	echo, err := tun.Listen(7)
	if err != nil {
		tun.Close()
		return nil, err
	}
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	dns, err := tun.ListenUDP(netip.AddrPortFrom(DNSAddr, 53))
	if err != nil {
		tun.Close()
		return nil, err
	}
	go serveDNS(dns)
	return p, nil
}

// Close stops the provider.
func (p *Provider) Close() { p.tun.Close() }

// ClientConfig returns the tunnel config a lane would use to connect with priv.
func (p *Provider) ClientConfig(priv string) wg.Config {
	return wg.Config{
		PrivateKey: priv,
		Addresses:  []netip.Addr{ClientAddr},
		DNS:        []netip.Addr{DNSAddr},
		PeerKey:    p.PublicKey,
		Endpoint:   p.Endpoint,
	}
}

func freeUDPPort() (int, error) {
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return 0, err
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port, nil
}

func serveDNS(pc net.PacketConn) {
	buf := make([]byte, 1500)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		var p dnsmessage.Parser
		h, err := p.Start(buf[:n])
		if err != nil {
			continue
		}
		q, err := p.Question()
		if err != nil {
			continue
		}
		b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: h.ID, Response: true, Authoritative: true})
		b.EnableCompression()
		b.StartQuestions()
		b.Question(q)
		b.StartAnswers()
		if q.Type == dnsmessage.TypeA {
			b.AResource(dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 60},
				dnsmessage.AResource{A: WebAddr.As4()})
		}
		msg, err := b.Finish()
		if err == nil {
			pc.WriteTo(msg, addr)
		}
	}
}
