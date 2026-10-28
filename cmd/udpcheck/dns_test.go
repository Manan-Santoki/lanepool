package main

import (
	"errors"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func TestGoogleMyAddrTXTQuerySetsRecursionDesired(t *testing.T) {
	q, id, err := googleMyAddrTXTQuery()
	if err != nil {
		t.Fatal(err)
	}
	if id != 0xface {
		t.Fatalf("id %x", id)
	}
	var msg dnsmessage.Message
	if err := msg.Unpack(q); err != nil {
		t.Fatal(err)
	}
	if !msg.Header.RecursionDesired {
		t.Fatal("RecursionDesired not set")
	}
	if len(msg.Questions) != 1 || msg.Questions[0].Type != dnsmessage.TypeTXT {
		t.Fatalf("questions %+v", msg.Questions)
	}
}

func TestExtractMyAddrIPFromDNSMatchesQueryID(t *testing.T) {
	const id = 0xabcd
	wrong, err := packDNSReply(id+1, "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := extractMyAddrIPFromDNS(wrong, id); !errors.Is(err, errSkipUDPReply) {
		t.Fatalf("wrong id: %v", err)
	}

	right, err := packDNSReply(id, "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	ip, err := extractMyAddrIPFromDNS(right, id)
	if err != nil || ip != "203.0.113.7" {
		t.Fatalf("ip %q err %v", ip, err)
	}
}

func packDNSReply(id uint16, txtIP string) ([]byte, error) {
	var msg dnsmessage.Message
	msg.Header.ID = id
	msg.Header.Response = true
	msg.Answers = []dnsmessage.Resource{{
		Header: dnsmessage.ResourceHeader{
			Name:  dnsmessage.MustNewName("o-o.myaddr.l.google.com."),
			Type:  dnsmessage.TypeTXT,
			Class: dnsmessage.ClassINET,
		},
		Body: &dnsmessage.TXTResource{TXT: []string{txtIP}},
	}}
	return msg.Pack()
}
