package gateway

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/Manan-Santoki/lanepool/internal/auth"
	"github.com/Manan-Santoki/lanepool/internal/engine/gateway/socks5"
	"github.com/Manan-Santoki/lanepool/internal/protocol"
	"github.com/Manan-Santoki/lanepool/internal/wg/wgtest"
)

func TestSOCKS5UDPAssociateDNS(t *testing.T) {
	e := newEnv(t, "nl")
	hash, _ := auth.HashPassword("password123")
	e.addUser(protocol.UserSpec{
		Username: "udpuser", Enabled: true, PasswordHash: hash,
		UDPEnabled: true, LogMode: protocol.LogModeOff,
	}, "password123")
	e.srv.UDP = UDPConfig{AdvertiseHost: "127.0.0.1", PortMin: 39000, PortMax: 39010}

	relayHost, relayPort, ctrl, err := socks5Associate(e.srv.Addr(), "udpuser", "password123")
	if err != nil {
		t.Fatal(err)
	}
	defer ctrl.Close()

	var msg dnsmessage.Message
	msg.Header.ID = 0x1234
	msg.Header.RecursionDesired = true
	msg.Questions = []dnsmessage.Question{{
		Name:  dnsmessage.MustNewName("any."),
		Type:  dnsmessage.TypeA,
		Class: dnsmessage.ClassINET,
	}}
	q, err := msg.Pack()
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := socks5.BuildUDP(wgtest.DNSAddr.String(), 53, q)
	if err != nil {
		t.Fatal(err)
	}
	uc, err := net.Dial("udp", net.JoinHostPort(relayHost, relayPort))
	if err != nil {
		t.Fatal(err)
	}
	defer uc.Close()
	if _, err := uc.Write(wrapped); err != nil {
		t.Fatal(err)
	}
	uc.SetReadDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 512)
	n, err := uc.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	_, payload, err := socks5.ParseUDP(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	var resp dnsmessage.Message
	if err := resp.Unpack(payload); err != nil {
		t.Fatal(err)
	}
	if len(resp.Answers) == 0 {
		t.Fatal("expected DNS answer")
	}
	ctrl.Close()
	time.Sleep(100 * time.Millisecond)
}

func socks5Associate(proxyAddr, user, pass string) (relayHost string, relayPort string, ctrl net.Conn, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var d net.Dialer
	ctrl, err = d.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return "", "", nil, err
	}
	if _, err = ctrl.Write([]byte{0x05, 0x01, 0x02}); err != nil {
		ctrl.Close()
		return "", "", nil, err
	}
	resp := make([]byte, 2)
	if _, err = io.ReadFull(ctrl, resp); err != nil {
		ctrl.Close()
		return "", "", nil, err
	}
	if resp[0] != 0x05 || resp[1] != 0x02 {
		ctrl.Close()
		return "", "", nil, fmt.Errorf("auth method: %v", resp)
	}
	u, p := []byte(user), []byte(pass)
	auth := []byte{0x01, byte(len(u))}
	auth = append(auth, u...)
	auth = append(auth, byte(len(p)))
	auth = append(auth, p...)
	if _, err = ctrl.Write(auth); err != nil {
		ctrl.Close()
		return "", "", nil, err
	}
	if _, err = io.ReadFull(ctrl, resp); err != nil {
		ctrl.Close()
		return "", "", nil, err
	}
	if resp[1] != 0x00 {
		ctrl.Close()
		return "", "", nil, fmt.Errorf("auth failed")
	}
	req := []byte{0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
	if _, err = ctrl.Write(req); err != nil {
		ctrl.Close()
		return "", "", nil, err
	}
	head := make([]byte, 4)
	if _, err = io.ReadFull(ctrl, head); err != nil {
		ctrl.Close()
		return "", "", nil, err
	}
	if head[1] != 0x00 {
		ctrl.Close()
		return "", "", nil, fmt.Errorf("associate failed code %d", head[1])
	}
	host, port, err := socks5.ReadAddrConn(head[3], ctrl)
	if err != nil {
		ctrl.Close()
		return "", "", nil, err
	}
	return host, strconv.Itoa(int(port)), ctrl, nil
}
