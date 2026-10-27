package wg_test

import (
	"context"
	"net"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/Manan-Santoki/lanepool/internal/wg"
	"github.com/Manan-Santoki/lanepool/internal/wg/wgtest"
)

func TestUDPThroughTunnel(t *testing.T) {
	priv, pub := wgtest.KeyPair()
	prov := wgtest.Start(t, "203.0.113.20", pub)

	tun, err := wg.Start(context.Background(), prov.ClientConfig(priv), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Close()

	pc, err := tun.ListenPacket(context.Background(), "udp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()

	var msg dnsmessage.Message
	msg.Header.ID = 0xabcd
	msg.Header.RecursionDesired = true
	msg.Questions = []dnsmessage.Question{{
		Name:  dnsmessage.MustNewName("test.example."),
		Type:  dnsmessage.TypeA,
		Class: dnsmessage.ClassINET,
	}}
	q, err := msg.Pack()
	if err != nil {
		t.Fatal(err)
	}
	dst := &net.UDPAddr{IP: wgtest.DNSAddr.AsSlice(), Port: 53}
	if _, err := pc.WriteTo(q, dst); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 512)
	pc.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, _, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	var resp dnsmessage.Message
	if err := resp.Unpack(buf[:n]); err != nil {
		t.Fatal(err)
	}
	if len(resp.Answers) == 0 {
		t.Fatalf("no DNS answers: %+v", resp)
	}
}
