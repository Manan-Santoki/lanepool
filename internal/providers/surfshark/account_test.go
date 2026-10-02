package surfshark

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoginErrors(t *testing.T) {
	cases := []struct {
		status   int
		body     string
		wantAuth bool
		contains string
	}{
		{401, `{"message":"bad credentials"}`, true, "bad credentials"},
		{403, `<!DOCTYPE html><title>Attention Required! | Cloudflare</title>`, false, "bot protection"},
		{429, `{"code":429,"message":"Too many requests"}`, false, "rate-limiting"},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
			w.Write([]byte(c.body))
		}))
		err := (&Account{BaseURL: srv.URL, Email: "a@b.c", Password: "x"}).Login(context.Background())
		srv.Close()
		if err == nil || errors.Is(err, ErrAuth) != c.wantAuth || !strings.Contains(err.Error(), c.contains) {
			t.Errorf("HTTP %d: got %v", c.status, err)
		}
	}
}
