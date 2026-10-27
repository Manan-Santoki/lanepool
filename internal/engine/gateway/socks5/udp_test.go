package socks5

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestParseBuildUDPIPv4(t *testing.T) {
	inner := []byte{0xde, 0xad, 0xbe, 0xef}
	raw, err := BuildUDP("198.51.100.7", 53, inner)
	if err != nil {
		t.Fatal(err)
	}
	h, payload, err := ParseUDP(raw)
	if err != nil {
		t.Fatal(err)
	}
	if h.Host != "198.51.100.7" || h.Port != 53 || !bytes.Equal(payload, inner) {
		t.Fatalf("got %+v payload %x", h, payload)
	}
}

func TestParseUDPRejectsFrag(t *testing.T) {
	raw, _ := BuildUDP("1.1.1.1", 53, []byte{1})
	raw[2] = 1
	_, _, err := ParseUDP(raw)
	if err != ErrFrag {
		t.Fatalf("expected ErrFrag, got %v", err)
	}
}

func TestParseUDPDomain(t *testing.T) {
	raw, err := BuildUDP("example.com", 1234, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	h, payload, err := ParseUDP(raw)
	if err != nil || h.Host != "example.com" || h.Port != 1234 || string(payload) != "x" {
		t.Fatalf("%+v %q %v", h, payload, err)
	}
}

func TestWriteBindAddr(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteBindAddr(&buf, "203.0.113.1", 30001); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	if b[0] != AddrIPv4 || binary.BigEndian.Uint16(b[5:7]) != 30001 {
		t.Fatalf("%x", b)
	}
}
