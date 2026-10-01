package surfshark

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

const key = "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk="

func srv(cc, loc string, virtual bool) Server {
	s := Server{Country: cc, CountryCode: cc, Location: loc, ConnectionName: cc + "-" + loc + ".prod.surfshark.com", PubKey: key}
	if virtual {
		s.Tags = []string{"virtual"}
	}
	return s
}

var servers = []Server{srv("us", "nyc", false), srv("us", "lax", false), srv("de", "fra", false), srv("de", "ber", true), srv("al", "tia", false)}

func ids(list []Server) []string {
	out := []string{}
	for _, s := range list {
		out = append(out, s.ID())
	}
	return out
}

func TestSelectSpreadsAcrossCountriesFirst(t *testing.T) {
	got := ids(Select(servers, Filter{IncludeVirtual: true}, 4))
	want := []string{"al-tia", "de-ber", "us-lax", "de-fra"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSelectFilters(t *testing.T) {
	got := ids(Select(servers, Filter{Countries: []string{"US", "de"}, ExcludeCountries: []string{"us"}}, 10))
	if !reflect.DeepEqual(got, []string{"de-fra"}) {
		t.Fatalf("got %v", got)
	}
}

func TestSelectPinnedLocationsKeepOrder(t *testing.T) {
	got := ids(Select(servers, Filter{Locations: []string{"us-nyc", "nope", "AL-TIA"}}, 10))
	if !reflect.DeepEqual(got, []string{"us-nyc", "al-tia"}) {
		t.Fatalf("got %v", got)
	}
}

func TestSelectLimit(t *testing.T) {
	if n := len(Select(servers, Filter{IncludeVirtual: true}, 2)); n != 2 {
		t.Fatalf("got %d", n)
	}
}

func TestTunnelConfig(t *testing.T) {
	c := TunnelConfig(servers[0], key)
	if c.Endpoint != "us-nyc.prod.surfshark.com:51820" || c.PeerKey != key || c.Addresses[0] != TunnelAddress {
		t.Fatalf("unexpected config %+v", c)
	}
}

func TestFetchSkipsInvalidEntries(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]Server{servers[0], {ConnectionName: "x.prod.surfshark.com", PubKey: "bad"}, {PubKey: key}})
	}))
	defer ts.Close()
	got, err := Fetch(context.Background(), ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID() != "us-nyc" {
		t.Fatalf("got %v", ids(got))
	}
}
