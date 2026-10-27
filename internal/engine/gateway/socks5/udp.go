// Package socks5 implements SOCKS5 UDP framing (RFC 1928).
package socks5

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
)

const (
	AddrIPv4   = 0x01
	AddrDomain = 0x03
	AddrIPv6   = 0x04
)

// Header is the SOCKS5 UDP request/response header before the payload.
type Header struct {
	Frag byte
	Host string
	Port uint16
}

var ErrFrag = errors.New("socks5 udp: fragmentation not supported")

// ParseUDP parses a SOCKS5 UDP datagram. The returned payload is the bytes after the header.
func ParseUDP(pkt []byte) (Header, []byte, error) {
	if len(pkt) < 4 {
		return Header{}, nil, errors.New("socks5 udp: packet too short")
	}
	if pkt[2] != 0 {
		return Header{}, nil, ErrFrag
	}
	h := Header{Frag: pkt[2]}
	off := 4
	switch pkt[3] {
	case AddrIPv4:
		if len(pkt) < off+4+2 {
			return Header{}, nil, errors.New("socks5 udp: truncated ipv4")
		}
		h.Host = netip.AddrFrom4([4]byte(pkt[off : off+4])).String()
		off += 4
	case AddrIPv6:
		if len(pkt) < off+16+2 {
			return Header{}, nil, errors.New("socks5 udp: truncated ipv6")
		}
		h.Host = netip.AddrFrom16([16]byte(pkt[off : off+16])).String()
		off += 16
	case AddrDomain:
		if len(pkt) < off+1 {
			return Header{}, nil, errors.New("socks5 udp: truncated domain len")
		}
		n := int(pkt[off])
		off++
		if len(pkt) < off+n+2 {
			return Header{}, nil, errors.New("socks5 udp: truncated domain")
		}
		h.Host = string(pkt[off : off+n])
		off += n
	default:
		return Header{}, nil, fmt.Errorf("socks5 udp: unknown address type %d", pkt[3])
	}
	h.Port = binary.BigEndian.Uint16(pkt[off : off+2])
	off += 2
	return h, pkt[off:], nil
}

// BuildUDP wraps payload in a SOCKS5 UDP header for the given destination.
func BuildUDP(host string, port uint16, payload []byte) ([]byte, error) {
	var addr []byte
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Is4() {
			b := ip.As4()
			addr = []byte{AddrIPv4, b[0], b[1], b[2], b[3]}
		} else {
			b := ip.As16()
			addr = make([]byte, 17)
			addr[0] = AddrIPv6
			copy(addr[1:], b[:])
		}
	} else {
		if len(host) > 255 {
			return nil, errors.New("socks5 udp: domain too long")
		}
		addr = make([]byte, 2+len(host))
		addr[0] = AddrDomain
		addr[1] = byte(len(host))
		copy(addr[2:], host)
	}
	out := make([]byte, 3+len(addr)+2+len(payload))
	out[0], out[1], out[2] = 0, 0, 0
	copy(out[3:], addr)
	off := 3 + len(addr)
	binary.BigEndian.PutUint16(out[off:off+2], port)
	copy(out[off+2:], payload)
	return out, nil
}

// ReadAddr parses a SOCKS5 address from a request (after VER CMD RSV ATYP).
func ReadAddr(atyp byte, r interface {
	Read([]byte) (int, error)
	ReadByte() (byte, error)
}) (host string, port uint16, err error) {
	switch atyp {
	case AddrIPv4:
		b := make([]byte, 4)
		if _, err := r.Read(b); err != nil {
			return "", 0, err
		}
		host = netip.AddrFrom4([4]byte(b)).String()
	case AddrIPv6:
		b := make([]byte, 16)
		if _, err := r.Read(b); err != nil {
			return "", 0, err
		}
		host = netip.AddrFrom16([16]byte(b)).String()
	case AddrDomain:
		n, err := r.ReadByte()
		if err != nil {
			return "", 0, err
		}
		b := make([]byte, n)
		if _, err := r.Read(b); err != nil {
			return "", 0, err
		}
		host = string(b)
	default:
		return "", 0, fmt.Errorf("unsupported address type %d", atyp)
	}
	pb := make([]byte, 2)
	if _, err := r.Read(pb); err != nil {
		return "", 0, err
	}
	port = binary.BigEndian.Uint16(pb)
	return host, port, nil
}

// ReadAddrConn reads a bind address from a TCP connection (uses bufio internally).
func ReadAddrConn(atyp byte, c net.Conn) (host string, port uint16, err error) {
	return ReadAddr(atyp, bufio.NewReader(c))
}

// WriteBindAddr writes a SOCKS5 reply bind address (ATYP + host + port).
func WriteBindAddr(w io.Writer, host string, port uint16) error {
	var part []byte
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Is4() {
			b := ip.As4()
			part = []byte{AddrIPv4, b[0], b[1], b[2], b[3]}
		} else {
			b := ip.As16()
			part = make([]byte, 17)
			part[0] = AddrIPv6
			copy(part[1:], b[:])
		}
	} else {
		if len(host) > 255 {
			return errors.New("bind host too long")
		}
		part = make([]byte, 2+len(host))
		part[0] = AddrDomain
		part[1] = byte(len(host))
		copy(part[2:], host)
	}
	out := make([]byte, len(part)+2)
	copy(out, part)
	binary.BigEndian.PutUint16(out[len(part):], port)
	_, err := w.Write(out)
	return err
}

// ClientUDPAddr extracts IP and port from a UDP client's address.
func ClientUDPAddr(a net.Addr) (netip.Addr, uint16, bool) {
	ua, ok := a.(*net.UDPAddr)
	if !ok {
		return netip.Addr{}, 0, false
	}
	ip, ok := netip.AddrFromSlice(ua.IP)
	if !ok {
		return netip.Addr{}, 0, false
	}
	return ip.Unmap(), uint16(ua.Port), true
}
