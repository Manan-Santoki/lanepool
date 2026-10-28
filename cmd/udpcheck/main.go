// udpcheck verifies lanepool SOCKS5 UDP ASSOCIATE from inside the Docker network.
//
// Usage:
//
//	udpcheck -proxy lanepool:8080 -user 'qbit-country-nl-session-qbit-sessttl-1440' -pass SECRET
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/proxy"

	"github.com/Manan-Santoki/lanepool/internal/engine/gateway/socks5"
)

func main() {
	proxyAddr := flag.String("proxy", "lanepool:8080", "SOCKS5 proxy host:port")
	user := flag.String("user", "", "proxy username")
	pass := flag.String("pass", "", "proxy password")
	dnsHost := flag.String("dns", "1.1.1.1", "DNS server for UDP query test (IPv4 ATYP)")
	dhtHost := flag.String("dht", "router.bittorrent.com", "DHT bootstrap host")
	flag.Parse()
	if *user == "" || *pass == "" {
		fmt.Fprintln(os.Stderr, "set -user and -pass")
		os.Exit(2)
	}

	exitIP, err := tcpExitIP(*proxyAddr, *user, *pass)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tcp exit ip: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("tcp exit IP:", exitIP)

	relayHost, relayPort, ctrl, err := associate(*proxyAddr, *user, *pass)
	if err != nil {
		fmt.Fprintf(os.Stderr, "associate: %v\n", err)
		os.Exit(1)
	}
	defer ctrl.Close()
	fmt.Printf("udp relay %s:%s (keep control connection open)\n", relayHost, relayPort)

	uc, err := net.Dial("udp", net.JoinHostPort(relayHost, relayPort))
	if err != nil {
		fmt.Fprintf(os.Stderr, "udp relay socket: %v\n", err)
		os.Exit(1)
	}
	defer uc.Close()

	if err := dnsCheck(uc, *dnsHost); err != nil {
		fmt.Fprintf(os.Stderr, "dns: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("dns: ok")

	if err := dnsDomainCheck(uc); err != nil {
		fmt.Fprintf(os.Stderr, "dns domain ATYP: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("dns domain ATYP (dns.google): ok")

	failed := false
	udpIP, err := udpExitIP(uc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "udp exit ip: %v (warning)\n", err)
		failed = true
	} else {
		fmt.Println("udp exit IP:", udpIP)
		if strings.TrimSpace(udpIP) == strings.TrimSpace(exitIP) {
			fmt.Println("tcp/udp exit IP: match")
		} else {
			fmt.Println("tcp/udp exit IP: MISMATCH")
			failed = true
		}
	}

	if err := dhtPing(uc, *dhtHost); err != nil {
		fmt.Fprintf(os.Stderr, "dht ping: %v (non-fatal)\n", err)
	} else {
		fmt.Println("dht ping: ok")
	}
	if failed {
		os.Exit(1)
	}
}

func tcpExitIP(proxyAddr, user, pass string) (string, error) {
	d, err := proxy.SOCKS5("tcp", proxyAddr, &proxy.Auth{User: user, Password: pass}, proxy.Direct)
	if err != nil {
		return "", err
	}
	c, err := d.Dial("tcp", "api.ipify.org:80")
	if err != nil {
		return "", err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(15 * time.Second))
	if _, err := io.WriteString(c, "GET / HTTP/1.1\r\nHost: api.ipify.org\r\nConnection: close\r\n\r\n"); err != nil {
		return "", err
	}
	buf := make([]byte, 4096)
	n, _ := io.ReadAtLeast(c, buf, 1)
	body := string(buf[:n])
	if i := strings.Index(body, "\r\n\r\n"); i >= 0 {
		return strings.TrimSpace(body[i+4:]), nil
	}
	return strings.TrimSpace(body), nil
}

func associate(proxyAddr, user, pass string) (string, string, net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctrl, err := (&net.Dialer{}).DialContext(ctx, "tcp", proxyAddr)
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
		return "", "", nil, fmt.Errorf("socks method: %v", resp)
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
	if _, err = io.ReadFull(ctrl, resp); err != nil || resp[1] != 0 {
		ctrl.Close()
		return "", "", nil, fmt.Errorf("socks auth failed")
	}
	if _, err = ctrl.Write([]byte{0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		ctrl.Close()
		return "", "", nil, err
	}
	head := make([]byte, 4)
	if _, err = io.ReadFull(ctrl, head); err != nil {
		ctrl.Close()
		return "", "", nil, err
	}
	if head[1] != 0 {
		ctrl.Close()
		return "", "", nil, fmt.Errorf("associate code %d", head[1])
	}
	host, port, err := socks5.ReadAddrConn(head[3], ctrl)
	if err != nil {
		ctrl.Close()
		return "", "", nil, err
	}
	return host, strconv.Itoa(int(port)), ctrl, nil
}

func dnsCheck(uc net.Conn, dnsServer string) error {
	const id = 0xbeef
	q, err := exampleAQuery(id, "example.com.")
	if err != nil {
		return err
	}
	wrapped, err := socks5.BuildUDP(dnsServer, 53, q)
	if err != nil {
		return err
	}
	return udpRoundTrip(uc, wrapped, func(payload []byte) error {
		if err := validateDNSReplyID(payload, id); err != nil {
			return err
		}
		var resp dnsmessage.Message
		return resp.Unpack(payload)
	})
}

func dnsDomainCheck(uc net.Conn) error {
	const id = 0xcafe
	q, err := exampleAQuery(id, "example.com.")
	if err != nil {
		return err
	}
	wrapped, err := socks5.BuildUDP("dns.google", 53, q)
	if err != nil {
		return err
	}
	return udpRoundTrip(uc, wrapped, func(payload []byte) error {
		if err := validateDNSReplyID(payload, id); err != nil {
			return err
		}
		var resp dnsmessage.Message
		return resp.Unpack(payload)
	})
}

func udpExitIP(uc net.Conn) (string, error) {
	q, id, err := googleMyAddrTXTQuery()
	if err != nil {
		return "", err
	}
	wrapped, err := socks5.BuildUDP("216.239.32.10", 53, q)
	if err != nil {
		return "", err
	}
	var ip string
	err = udpRoundTrip(uc, wrapped, func(payload []byte) error {
		got, err := extractMyAddrIPFromDNS(payload, id)
		if err != nil {
			return err
		}
		ip = got
		return nil
	})
	if err != nil {
		return "", err
	}
	return ip, nil
}

func dhtPing(uc net.Conn, host string) error {
	payload := []byte("d1:ad2:id20:01234567890123456789e1:q4:ping1:t2:aa1:y1:qe")
	wrapped, err := socks5.BuildUDP(host, 6881, payload)
	if err != nil {
		return err
	}
	return udpRoundTrip(uc, wrapped, func(reply []byte) error {
		if len(reply) == 0 || reply[0] != 'd' {
			return fmt.Errorf("unexpected dht reply %q", reply)
		}
		return nil
	})
}

func udpRoundTrip(uc net.Conn, wrapped []byte, check func([]byte) error) error {
	deadline := time.Now().Add(15 * time.Second)
	if _, err := uc.Write(wrapped); err != nil {
		return err
	}
	buf := make([]byte, 1500)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for udp reply")
		}
		uc.SetDeadline(time.Now().Add(time.Until(deadline)))
		n, err := uc.Read(buf)
		if err != nil {
			return err
		}
		_, payload, err := socks5.ParseUDP(buf[:n])
		if err != nil {
			continue
		}
		if err := check(payload); errors.Is(err, errSkipUDPReply) {
			continue
		} else if err != nil {
			return err
		}
		return nil
	}
}
