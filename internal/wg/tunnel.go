// Package wg runs userspace WireGuard tunnels (wireguard-go + gVisor netstack)
// inside the lanepool process. Each lane owns one Tunnel; connections are dialled
// through it without touching the host's network configuration.
package wg

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

// Config describes one WireGuard peer connection (a wg-quick [Interface] + one [Peer]).
type Config struct {
	PrivateKey   string         // base64
	Addresses    []netip.Addr   // tunnel addresses, e.g. 10.14.0.2
	DNS          []netip.Addr   // resolvers reached through the tunnel
	MTU          int            // 0 = 1420
	PeerKey      string         // base64 public key of the server
	PresharedKey string         // base64, optional
	Endpoint     string         // host:port; host names are resolved when the tunnel starts
	AllowedIPs   []netip.Prefix // nil = 0.0.0.0/0 and ::/0
	Keepalive    time.Duration  // 0 = 25s
	ListenPort   int            // 0 = random; only used by test servers
}

// Stats is a snapshot of the peer's state from the WireGuard device.
type Stats struct {
	LastHandshake time.Time // zero if no handshake has completed
	RxBytes       uint64
	TxBytes       uint64
}

// Tunnel is one running userspace WireGuard device.
type Tunnel struct {
	dev  *device.Device
	net  *netstack.Net
	once sync.Once
}

// ParseKey decodes a base64 WireGuard key and returns it hex encoded, as the
// device configuration protocol expects.
func ParseKey(b64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil || len(raw) != 32 {
		return "", errors.New("invalid WireGuard key")
	}
	return hex.EncodeToString(raw), nil
}

// ValidKey reports whether s is a base64 encoded 32-byte WireGuard key.
func ValidKey(s string) bool {
	_, err := ParseKey(s)
	return err == nil
}

// Start creates the device, configures the peer and brings it up. With a
// keepalive set, WireGuard starts the handshake immediately.
func Start(ctx context.Context, cfg Config, logf func(format string, args ...any)) (*Tunnel, error) {
	if len(cfg.Addresses) == 0 {
		return nil, errors.New("wg: no tunnel address")
	}
	mtu := cfg.MTU
	if mtu == 0 {
		mtu = 1420
	}
	priv, err := ParseKey(cfg.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("private key: %w", err)
	}
	pub, err := ParseKey(cfg.PeerKey)
	if err != nil {
		return nil, fmt.Errorf("peer key: %w", err)
	}

	var uapi strings.Builder
	fmt.Fprintf(&uapi, "private_key=%s\n", priv)
	if cfg.ListenPort > 0 {
		fmt.Fprintf(&uapi, "listen_port=%d\n", cfg.ListenPort)
	}
	fmt.Fprintf(&uapi, "public_key=%s\n", pub)
	if cfg.PresharedKey != "" {
		psk, err := ParseKey(cfg.PresharedKey)
		if err != nil {
			return nil, fmt.Errorf("preshared key: %w", err)
		}
		fmt.Fprintf(&uapi, "preshared_key=%s\n", psk)
	}
	if cfg.Endpoint != "" {
		ep, err := resolveEndpoint(ctx, cfg.Endpoint)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&uapi, "endpoint=%s\n", ep)
	}
	keepalive := cfg.Keepalive
	if keepalive == 0 {
		keepalive = 25 * time.Second
	}
	if keepalive > 0 {
		fmt.Fprintf(&uapi, "persistent_keepalive_interval=%d\n", int(keepalive/time.Second))
	}
	allowed := cfg.AllowedIPs
	if allowed == nil {
		allowed = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}
	}
	for _, p := range allowed {
		fmt.Fprintf(&uapi, "allowed_ip=%s\n", p)
	}

	tdev, tnet, err := netstack.CreateNetTUN(cfg.Addresses, cfg.DNS, mtu)
	if err != nil {
		return nil, fmt.Errorf("netstack: %w", err)
	}
	logger := &device.Logger{Verbosef: device.DiscardLogf, Errorf: device.DiscardLogf}
	if logf != nil {
		logger.Errorf = logf
	}
	dev := device.NewDevice(tdev, conn.NewDefaultBind(), logger)
	if err := dev.IpcSet(uapi.String()); err != nil {
		dev.Close()
		return nil, fmt.Errorf("configure device: %w", err)
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return nil, fmt.Errorf("device up: %w", err)
	}
	return &Tunnel{dev: dev, net: tnet}, nil
}

func resolveEndpoint(ctx context.Context, endpoint string) (string, error) {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return "", fmt.Errorf("endpoint %q: %w", endpoint, err)
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return netip.AddrPortFrom(ip, mustPort(port)).String(), nil
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
	if err != nil || len(ips) == 0 {
		return "", fmt.Errorf("resolve endpoint %s: %w", host, err)
	}
	return netip.AddrPortFrom(ips[0].Unmap(), mustPort(port)).String(), nil
}

func mustPort(s string) uint16 {
	p, _ := strconv.ParseUint(s, 10, 16)
	return uint16(p)
}

// DialContext opens a TCP connection through the tunnel. Host names are resolved
// with the tunnel's DNS servers, so lookups don't leak to the host resolver.
func (t *Tunnel) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return t.net.DialContext(ctx, network, address)
}

// Listen opens a TCP listener on the tunnel address (used by test servers).
func (t *Tunnel) Listen(port int) (net.Listener, error) {
	return t.net.ListenTCP(&net.TCPAddr{Port: port})
}

// ListenUDP opens a UDP socket on one of the tunnel addresses (used by test DNS
// servers). Binding to a specific address makes replies come from that address.
func (t *Tunnel) ListenUDP(addr netip.AddrPort) (net.PacketConn, error) {
	return t.net.ListenUDPAddrPort(addr)
}

// Stats reads the peer state from the device.
func (t *Tunnel) Stats() Stats {
	var s Stats
	text, err := t.dev.IpcGet()
	if err != nil {
		return s
	}
	var sec, nsec int64
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch k {
		case "last_handshake_time_sec":
			sec, _ = strconv.ParseInt(v, 10, 64)
		case "last_handshake_time_nsec":
			nsec, _ = strconv.ParseInt(v, 10, 64)
		case "rx_bytes":
			s.RxBytes, _ = strconv.ParseUint(v, 10, 64)
		case "tx_bytes":
			s.TxBytes, _ = strconv.ParseUint(v, 10, 64)
		}
	}
	if sec > 0 {
		s.LastHandshake = time.Unix(sec, nsec)
	}
	return s
}

// Close tears the device down. WireGuard has no disconnect message: the server
// keeps the session until it times out on its side.
func (t *Tunnel) Close() {
	t.once.Do(func() { t.dev.Close() })
}
