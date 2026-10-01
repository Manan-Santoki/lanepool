// Package gateway is lanepool's proxy server: HTTP (CONNECT and plain requests)
// and SOCKS5 on one port, with per-user authentication, limits and lane selection.
package gateway

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pires/go-proxyproto"

	"github.com/Manan-Santoki/lanepool/internal/engine/lanes"
	"github.com/Manan-Santoki/lanepool/internal/protocol"
)

// Lanes is what the gateway needs from the lane manager.
type Lanes interface {
	Usable() []lanes.Info
	Dial(ctx context.Context, id, address string) (net.Conn, func(), error)
}

// Server is the proxy listener.
type Server struct {
	Policy *Policy
	Lanes  Lanes
	Log    *slog.Logger
	// TrustedProxies may send a PROXY protocol header carrying the real client
	// address (e.g. Traefik in front of the gateway).
	TrustedProxies []netip.Prefix

	settings atomic.Pointer[protocol.EngineSettings]
	rr       atomic.Uint64

	mu       sync.Mutex
	ln       net.Listener
	addr     string
	live     map[string]*liveConn
	records  []protocol.ConnRecord
	burnedEv []protocol.BurnedIP
}

type liveConn struct {
	info   protocol.LiveConn
	up     atomic.Int64
	down   atomic.Int64
	cancel context.CancelFunc
	kicked atomic.Bool
}

// SetSettings updates the tunables used for new connections.
func (s *Server) SetSettings(st protocol.EngineSettings) { s.settings.Store(&st) }

func (s *Server) cfg() protocol.EngineSettings {
	if p := s.settings.Load(); p != nil {
		return *p
	}
	return protocol.DefaultSettings()
}

// Listen starts accepting connections on addr.
func (s *Server) Listen(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	if len(s.TrustedProxies) > 0 {
		ln = &proxyproto.Listener{Listener: ln, ConnPolicy: s.proxyPolicy}
	}
	s.mu.Lock()
	s.ln, s.addr = ln, addr
	if s.live == nil {
		s.live = map[string]*liveConn{}
	}
	s.mu.Unlock()
	go s.serve(ln)
	return nil
}

func (s *Server) proxyPolicy(opts proxyproto.ConnPolicyOptions) (proxyproto.Policy, error) {
	ip := clientAddr(opts.Upstream)
	for _, p := range s.TrustedProxies {
		if p.Contains(ip) {
			return proxyproto.USE, nil
		}
	}
	return proxyproto.IGNORE, nil
}

// Addr is the listening address (useful with port 0 in tests).
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// Listening reports whether the listener is open.
func (s *Server) Listening() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ln != nil
}

// Restart closes the listener and every open connection, then listens again.
// Lanes are not touched.
func (s *Server) Restart() error {
	s.mu.Lock()
	ln, addr := s.ln, s.addr
	s.ln = nil
	for _, c := range s.live {
		c.cancel()
	}
	s.mu.Unlock()
	if ln != nil {
		ln.Close()
	}
	return s.Listen(addr)
}

// Close stops listening and closes all connections.
func (s *Server) Close() {
	s.mu.Lock()
	ln := s.ln
	s.ln = nil
	for _, c := range s.live {
		c.cancel()
	}
	s.mu.Unlock()
	if ln != nil {
		ln.Close()
	}
}

func (s *Server) serve(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		go s.handle(c)
	}
}

// Live returns the open connections, newest first.
func (s *Server) Live() []protocol.LiveConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]protocol.LiveConn, 0, len(s.live))
	for _, c := range s.live {
		info := c.info
		info.BytesUp, info.BytesDown = c.up.Load(), c.down.Load()
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// Kick closes connections by ID and/or user. It returns how many were closed.
func (s *Server) Kick(req protocol.KickRequest) int {
	ids := map[string]bool{}
	for _, id := range req.IDs {
		ids[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, c := range s.live {
		if ids[id] || (req.UserID != 0 && c.info.UserID == req.UserID) {
			c.kicked.Store(true)
			c.cancel()
			n++
		}
	}
	return n
}

// DrainRecords returns finished connection records and auto-burned IPs since the last call.
func (s *Server) DrainRecords() ([]protocol.ConnRecord, []protocol.BurnedIP) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, b := s.records, s.burnedEv
	s.records, s.burnedEv = nil, nil
	return r, b
}

func (s *Server) record(r protocol.ConnRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.records) < 50000 { // bounded if control is unreachable for long
		s.records = append(s.records, r)
	}
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// request is one proxied connection being set up.
type request struct {
	rec    protocol.ConnRecord
	client netip.Addr
	user   *user
	params Params
	host   string
	port   string
}

func (s *Server) handle(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(30 * time.Second)) // handshake/auth must be quick
	br := bufio.NewReader(c)
	first, err := br.Peek(1)
	if err != nil {
		return
	}
	req := &request{client: clientAddr(c.RemoteAddr())}
	req.rec = protocol.ConnRecord{ID: newID(), ClientIP: req.client.String(), StartedAt: time.Now()}
	if first[0] == 0x05 {
		req.rec.Protocol = "socks5"
		s.handleSOCKS(c, br, req)
	} else {
		s.handleHTTP(c, br, req)
	}
}

// admit authenticates and authorises a request and picks a lane. On failure it
// returns the result code; the caller reports it in protocol-specific form.
func (s *Server) admit(req *request, username, password string) (laneID string, release func(), result string) {
	if s.cfg().Paused {
		return "", nil, protocol.ResultPaused
	}
	u, params, res := s.Policy.Authenticate(username, password, req.client)
	req.params = params
	req.rec.Username = params.Username
	if u != nil {
		req.user = u
		req.rec.UserID = u.spec.ID
		if u.spec.LogDestinations {
			req.rec.Target = net.JoinHostPort(req.host, req.port)
		}
	}
	if res != "" {
		return "", nil, res
	}
	if !u.targetAllowed(req.host) {
		return "", nil, protocol.ResultDenied
	}
	release, res = s.Policy.Admit(u)
	if res != "" {
		return "", nil, res
	}
	laneID = s.selectLane(req)
	if laneID == "" {
		release()
		return "", nil, protocol.ResultNoLane
	}
	return laneID, release, ""
}

// selectLane picks a lane for the request, honouring sticky sessions, user
// restrictions, username parameters and burned IPs.
func (s *Server) selectLane(req *request) string {
	u, p := req.user, req.params
	var cands []lanes.Info
	for _, l := range s.Lanes.Usable() {
		if len(u.lanes) > 0 && !u.lanes[strings.ToLower(l.ID)] {
			continue
		}
		if len(u.countries) > 0 && !u.countries[l.CountryCode] {
			continue
		}
		if p.Country != "" && l.CountryCode != p.Country {
			continue
		}
		if p.Lane != "" && !strings.EqualFold(l.ID, p.Lane) && !strings.HasSuffix(strings.ToLower(l.ID), ":"+strings.ToLower(p.Lane)) {
			continue
		}
		if s.Policy.Burned(l.ID, req.host) {
			continue
		}
		cands = append(cands, l)
	}
	if len(cands) == 0 {
		return ""
	}

	key, ttl := stickyKey(u, p, req.client)
	if key != "" {
		if id := s.Policy.stickyGet(key); id != "" {
			for _, c := range cands {
				if c.ID == id {
					s.Policy.stickySet(key, id, ttl)
					return id
				}
			}
		}
	}
	id := pick(cands, s.cfg().Strategy, &s.rr)
	if key != "" {
		s.Policy.stickySet(key, id, ttl)
	}
	return id
}

func pick(cands []lanes.Info, strategy string, rr *atomic.Uint64) string {
	switch strategy {
	case protocol.StrategyRandom:
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(cands))))
		return cands[n.Int64()].ID
	case protocol.StrategyLeastConnections:
		best := cands[0]
		for _, c := range cands[1:] {
			if c.Active < best.Active {
				best = c
			}
		}
		return best.ID
	case protocol.StrategyLowestLatency:
		best := cands[0]
		for _, c := range cands[1:] {
			if c.Latency > 0 && (best.Latency == 0 || c.Latency < best.Latency) {
				best = c
			}
		}
		return best.ID
	default:
		return cands[int(rr.Add(1)-1)%len(cands)].ID
	}
}

// dial connects to the target through the chosen lane and feeds burned-IP detection.
func (s *Server) dial(ctx context.Context, req *request, laneID string) (net.Conn, func(), error) {
	timeout := time.Duration(s.cfg().DialTimeout) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, release, err := s.Lanes.Dial(dctx, laneID, net.JoinHostPort(req.host, req.port))
	if b := s.Policy.DialResult(laneID, req.host, err == nil); b != nil {
		s.mu.Lock()
		s.burnedEv = append(s.burnedEv, *b)
		s.mu.Unlock()
	}
	return conn, release, err
}

// finish records a connection that ended (or was rejected).
func (s *Server) finish(req *request, result string, err error) {
	req.rec.EndedAt = time.Now()
	req.rec.Result = result
	if err != nil && result != protocol.ResultOK {
		req.rec.Error = err.Error()
	}
	if req.user != nil {
		s.Policy.CountConnection(req.user.spec.ID, req.rec.LaneID, result != protocol.ResultOK)
	}
	s.record(req.rec)
}

// relay pipes data between client and upstream until either side closes, the
// connection is kicked or it goes idle. Bytes count towards the user's usage.
// sentUp is data already written to upstream before relaying (a forwarded request).
func (s *Server) relay(ctx context.Context, req *request, client net.Conn, clientBuf io.Reader, upstream net.Conn, sentUp int64) string {
	ctx, cancel := context.WithCancel(ctx)
	lc := &liveConn{cancel: cancel, info: protocol.LiveConn{
		ID: req.rec.ID, UserID: req.rec.UserID, Username: req.rec.Username, ClientIP: req.rec.ClientIP,
		Target: req.rec.Target, LaneID: req.rec.LaneID, ExitIP: req.rec.ExitIP, Protocol: req.rec.Protocol,
		StartedAt: req.rec.StartedAt,
	}}
	s.mu.Lock()
	s.live[req.rec.ID] = lc
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.live, req.rec.ID)
		s.mu.Unlock()
	}()

	client.SetDeadline(time.Time{})
	idle := time.Duration(s.cfg().IdleTimeout) * time.Second
	if idle <= 0 {
		idle = 5 * time.Minute
	}
	var lastActive atomic.Int64
	lastActive.Store(time.Now().UnixNano())
	uid, lane := req.rec.UserID, req.rec.LaneID
	if sentUp > 0 {
		lc.up.Add(sentUp)
		s.Policy.AddUsage(uid, lane, sentUp, 0)
	}

	copyCount := func(dst net.Conn, src io.Reader, counter *atomic.Int64, up bool) {
		buf := make([]byte, 32*1024)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				lastActive.Store(time.Now().UnixNano())
				counter.Add(int64(n))
				if up {
					s.Policy.AddUsage(uid, lane, int64(n), 0)
				} else {
					s.Policy.AddUsage(uid, lane, 0, int64(n))
				}
				if _, werr := dst.Write(buf[:n]); werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		cancel()
	}
	go copyCount(upstream, clientBuf, &lc.up, true)
	go copyCount(client, upstream, &lc.down, false)

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	result := protocol.ResultOK
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-ticker.C:
			if time.Since(time.Unix(0, lastActive.Load())) > idle {
				break loop
			}
		}
	}
	client.Close()
	upstream.Close()
	if lc.kicked.Load() {
		result = protocol.ResultKicked
	}
	req.rec.BytesUp, req.rec.BytesDown = lc.up.Load(), lc.down.Load()
	return result
}

func (s *Server) laneExit(laneID string) string {
	for _, l := range s.Lanes.Usable() {
		if l.ID == laneID {
			return l.ExitIP
		}
	}
	return ""
}

// --- HTTP -------------------------------------------------------------------

func (s *Server) handleHTTP(c net.Conn, br *bufio.Reader, req *request) {
	hr, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	if hr.Method == http.MethodConnect {
		req.rec.Protocol = "connect"
		req.host, req.port, err = net.SplitHostPort(hr.Host)
	} else {
		req.rec.Protocol = "http"
		if hr.URL.Host == "" {
			writeHTTPError(c, http.StatusBadRequest, "lanepool is a proxy; send absolute URLs or CONNECT")
			return
		}
		req.host, req.port = hr.URL.Hostname(), hr.URL.Port()
		if req.port == "" {
			req.port = "80"
		}
	}
	if err != nil || req.host == "" {
		writeHTTPError(c, http.StatusBadRequest, "bad target")
		return
	}
	user, pass, ok := proxyBasicAuth(hr)
	if !ok {
		c.Write([]byte("HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"lanepool\"\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
		return
	}
	laneID, release, res := s.admit(req, user, pass)
	if res != "" {
		writeHTTPError(c, httpStatus(res), res)
		s.finish(req, res, nil)
		return
	}
	defer release()
	req.rec.LaneID = laneID
	req.rec.ExitIP = s.laneExit(laneID)

	ctx := context.Background()
	upstream, releaseLane, err := s.dial(ctx, req, laneID)
	if err != nil {
		writeHTTPError(c, http.StatusBadGateway, "target unreachable through lane")
		s.finish(req, protocol.ResultDialFailed, err)
		return
	}
	defer releaseLane()

	var sent int64
	if hr.Method == http.MethodConnect {
		c.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
	} else {
		// Plain HTTP: forward this request; one request per connection.
		hr.Header.Del("Proxy-Authorization")
		hr.Header.Del("Proxy-Connection")
		hr.Header.Set("Connection", "close")
		hr.RequestURI = ""
		cw := &countWriter{w: upstream}
		if err := hr.Write(cw); err != nil {
			upstream.Close()
			s.finish(req, protocol.ResultDialFailed, err)
			return
		}
		sent = cw.n
	}
	res = s.relay(ctx, req, c, br, upstream, sent)
	s.finish(req, res, nil)
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func proxyBasicAuth(r *http.Request) (string, string, bool) {
	h := r.Header.Get("Proxy-Authorization")
	const prefix = "Basic "
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", "", false
	}
	raw, err := base64.StdEncoding.DecodeString(h[len(prefix):])
	if err != nil {
		return "", "", false
	}
	u, p, ok := strings.Cut(string(raw), ":")
	return u, p, ok
}

func httpStatus(result string) int {
	switch result {
	case protocol.ResultAuthFailed:
		return http.StatusProxyAuthRequired
	case protocol.ResultDenied:
		return http.StatusForbidden
	case protocol.ResultQuota:
		return http.StatusPaymentRequired
	case protocol.ResultRateLimited:
		return http.StatusTooManyRequests
	case protocol.ResultNoLane, protocol.ResultPaused:
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadGateway
	}
}

func writeHTTPError(c net.Conn, status int, msg string) {
	body := msg + "\n"
	extra := ""
	if status == http.StatusProxyAuthRequired {
		extra = "Proxy-Authenticate: Basic realm=\"lanepool\"\r\n"
	}
	fmt.Fprintf(c, "HTTP/1.1 %d %s\r\n%sContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\nX-Lanepool-Error: %s\r\n\r\n%s",
		status, http.StatusText(status), extra, len(body), msg, body)
}

// --- SOCKS5 -----------------------------------------------------------------

const (
	socksOK             = 0x00
	socksFailure        = 0x01
	socksNotAllowed     = 0x02
	socksNetUnreachable = 0x03
	socksHostUnreach    = 0x04
	socksCmdUnsupported = 0x07
	socksAddrUnsupport  = 0x08
)

func (s *Server) handleSOCKS(c net.Conn, br *bufio.Reader, req *request) {
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(br, hdr); err != nil {
		return
	}
	methods := make([]byte, hdr[1])
	if _, err := io.ReadFull(br, methods); err != nil {
		return
	}
	hasUserPass := false
	for _, m := range methods {
		if m == 0x02 {
			hasUserPass = true
		}
	}
	if !hasUserPass {
		c.Write([]byte{0x05, 0xFF}) // username/password is required
		return
	}
	c.Write([]byte{0x05, 0x02})

	// RFC 1929 username/password.
	ver := make([]byte, 2)
	if _, err := io.ReadFull(br, ver); err != nil {
		return
	}
	uname := make([]byte, ver[1])
	if _, err := io.ReadFull(br, uname); err != nil {
		return
	}
	plen, err := br.ReadByte()
	if err != nil {
		return
	}
	passwd := make([]byte, plen)
	if _, err := io.ReadFull(br, passwd); err != nil {
		return
	}

	// The client sends its request only after a successful auth reply, so check
	// the credentials now. Account rules (disabled, quota, ...) are applied in
	// admit once the target is known, and reported with a SOCKS error code.
	if u, _, res := s.Policy.Authenticate(string(uname), string(passwd), req.client); res == protocol.ResultAuthFailed || u == nil {
		c.Write([]byte{0x01, 0x01})
		req.rec.Username = ParseUsername(string(uname)).Username
		s.finish(req, protocol.ResultAuthFailed, nil)
		return
	}
	c.Write([]byte{0x01, 0x00})

	// Request: VER CMD RSV ATYP DST.ADDR DST.PORT.

	head := make([]byte, 4)
	if _, err := io.ReadFull(br, head); err != nil {
		return
	}
	if head[1] != 0x01 {
		socksReply(c, socksCmdUnsupported)
		return
	}
	switch head[3] {
	case 0x01:
		b := make([]byte, 4)
		if _, err := io.ReadFull(br, b); err != nil {
			return
		}
		req.host = netip.AddrFrom4([4]byte(b)).String()
	case 0x04:
		b := make([]byte, 16)
		if _, err := io.ReadFull(br, b); err != nil {
			return
		}
		req.host = netip.AddrFrom16([16]byte(b)).String()
	case 0x03:
		n, err := br.ReadByte()
		if err != nil {
			return
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(br, b); err != nil {
			return
		}
		req.host = string(b)
	default:
		socksReply(c, socksAddrUnsupport)
		return
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(br, pb); err != nil {
		return
	}
	req.port = strconv.Itoa(int(binary.BigEndian.Uint16(pb)))

	laneID, release, res := s.admit(req, string(uname), string(passwd))
	if res != "" {
		socksReply(c, socksCode(res))
		s.finish(req, res, nil)
		return
	}
	defer release()
	req.rec.LaneID = laneID
	req.rec.ExitIP = s.laneExit(laneID)

	ctx := context.Background()
	upstream, releaseLane, err := s.dial(ctx, req, laneID)
	if err != nil {
		socksReply(c, socksHostUnreach)
		s.finish(req, protocol.ResultDialFailed, err)
		return
	}
	defer releaseLane()
	socksReply(c, socksOK)
	res = s.relay(ctx, req, c, br, upstream, 0)
	s.finish(req, res, nil)
}

func socksReply(c net.Conn, code byte) {
	c.Write([]byte{0x05, code, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
}

func socksCode(result string) byte {
	switch result {
	case protocol.ResultDenied, protocol.ResultQuota, protocol.ResultRateLimited:
		return socksNotAllowed
	case protocol.ResultNoLane, protocol.ResultPaused:
		return socksNetUnreachable
	default:
		return socksFailure
	}
}
