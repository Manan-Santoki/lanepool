package main

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/net/dns/dnsmessage"
)

// errSkipUDPReply tells udpRoundTrip to keep reading datagrams.
var errSkipUDPReply = errors.New("skip udp reply")

func packDNSQuery(id uint16, name string, qtype dnsmessage.Type, rd bool) ([]byte, error) {
	var msg dnsmessage.Message
	msg.Header.ID = id
	msg.Header.RecursionDesired = rd
	msg.Questions = []dnsmessage.Question{{
		Name:  dnsmessage.MustNewName(name),
		Type:  qtype,
		Class: dnsmessage.ClassINET,
	}}
	return msg.Pack()
}

func googleMyAddrTXTQuery() ([]byte, uint16, error) {
	const id = 0xface
	q, err := packDNSQuery(id, "o-o.myaddr.l.google.com.", dnsmessage.TypeTXT, true)
	return q, id, err
}

func exampleAQuery(id uint16, name string) ([]byte, error) {
	return packDNSQuery(id, name, dnsmessage.TypeA, true)
}

// extractMyAddrIPFromDNS parses a DNS reply to Google's openresolver TXT
// endpoint and returns the embedded client IP. queryID must match the reply ID.
func extractMyAddrIPFromDNS(payload []byte, queryID uint16) (string, error) {
	var resp dnsmessage.Message
	if err := resp.Unpack(payload); err != nil {
		return "", err
	}
	if resp.Header.ID != queryID {
		return "", errSkipUDPReply
	}
	for _, a := range resp.Answers {
		txt, ok := a.Body.(*dnsmessage.TXTResource)
		if !ok {
			continue
		}
		for _, s := range txt.TXT {
			if strings.Contains(s, ".") && !strings.Contains(s, " ") {
				return strings.TrimSpace(s), nil
			}
		}
	}
	return "", fmt.Errorf("no TXT in response")
}

// validateDNSReplyID returns errSkipUDPReply when the DNS header ID does not match.
func validateDNSReplyID(payload []byte, queryID uint16) error {
	if len(payload) < 2 {
		return errSkipUDPReply
	}
	if uint16(payload[0])<<8|uint16(payload[1]) != queryID {
		return errSkipUDPReply
	}
	return nil
}
