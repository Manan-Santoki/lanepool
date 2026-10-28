package gateway

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Manan-Santoki/lanepool/internal/engine/gateway/socks5"
	"github.com/Manan-Santoki/lanepool/internal/protocol"
)

// UDPConfig configures SOCKS5 UDP ASSOCIATE relay ports and advertised bind address.
type UDPConfig struct {
	AdvertiseHost string
	AdvertiseIP   netip.Addr // optional; overrides host in reply when set
	PortMin       int
	PortMax       int
}

type portPool struct {
	mu    sync.Mutex
	min   int
	max   int
	used  map[int]bool
	alloc map[*net.UDPConn]int
}

func newPortPool(min, max int) *portPool {
	if min <= 0 || max < min {
		min, max = 30000, 30100
	}
	return &portPool{min: min, max: max, used: map[int]bool{}, alloc: map[*net.UDPConn]int{}}
}

func (p *portPool) acquire() (*net.UDPConn, int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for port := p.min; port <= p.max; port++ {
		if p.used[port] {
			continue
		}
		c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: port})
		if err != nil {
			continue
		}
		p.used[port] = true
		p.alloc[c] = port
		return c, port, nil
	}
	return nil, 0, errors.New("no udp relay ports available")
}

func (p *portPool) release(c *net.UDPConn) {
	if c == nil {
		return
	}
	p.mu.Lock()
	if port, ok := p.alloc[c]; ok {
		delete(p.used, port)
		delete(p.alloc, c)
	}
	p.mu.Unlock()
	c.Close()
}

func (s *Server) ensurePortPool() *portPool {
	if s.udpPorts == nil {
		s.udpPorts = newPortPool(s.UDP.PortMin, s.UDP.PortMax)
	}
	return s.udpPorts
}

func (s *Server) advertiseHost() string {
	if s.UDP.AdvertiseIP.IsValid() {
		return s.UDP.AdvertiseIP.String()
	}
	if h := s.UDP.AdvertiseHost; h != "" {
		return h
	}
	return "127.0.0.1"
}

func (s *Server) userIdleTimeout(u *user) time.Duration {
	sec := u.spec.IdleTimeoutSec
	if sec <= 0 {
		sec = s.cfg().IdleTimeout
	}
	if sec <= 0 {
		return 5 * time.Minute
	}
	return time.Duration(sec) * time.Second
}

func (s *Server) shouldLogConn(u *user) bool {
	switch u.spec.LogMode {
	case protocol.LogModeOff:
		return false
	case protocol.LogModeSampled:
		return time.Now().UnixNano()%100 == 0
	default:
		return true
	}
}

func (s *Server) handleSOCKSAssociate(c net.Conn, req *request, username, password string) {
	u, params, res := s.Policy.Authenticate(username, password, req.client)
	req.params = params
	req.rec.Username = params.Username
	req.rec.Protocol = "socks5-udp"
	if res == protocol.ResultAuthFailed || u == nil {
		socksReply(c, socksFailure)
		s.finish(req, protocol.ResultAuthFailed, nil)
		return
	}
	req.user = u
	req.rec.UserID = u.spec.ID
	if !u.spec.UDPEnabled {
		socksReply(c, socksNotAllowed)
		s.finish(req, protocol.ResultDenied, nil)
		return
	}
	if s.cfg().Paused {
		socksReply(c, socksNetUnreachable)
		s.finish(req, protocol.ResultPaused, nil)
		return
	}
	releaseAdmit, res := s.Policy.Admit(u)
	if res != "" {
		socksReply(c, socksCode(res))
		s.finish(req, res, nil)
		return
	}

	req.host = ""
	req.port = ""
	laneID := s.selectLane(req)
	if laneID == "" {
		releaseAdmit()
		socksReply(c, socksNetUnreachable)
		s.finish(req, protocol.ResultNoLane, nil)
		return
	}
	req.rec.LaneID = laneID
	req.rec.ExitIP = s.laneExit(laneID)

	ctx := context.Background()
	lanePC, releaseLane, err := s.Lanes.ListenPacket(ctx, laneID)
	if err != nil {
		releaseAdmit()
		socksReply(c, socksHostUnreach)
		s.finish(req, protocol.ResultDialFailed, err)
		return
	}

	relayConn, relayPort, err := s.ensurePortPool().acquire()
	if err != nil {
		releaseLane()
		releaseAdmit()
		socksReply(c, socksFailure)
		s.finish(req, protocol.ResultDialFailed, err)
		return
	}

	if err := socksAssociateReply(c, s.advertiseHost(), relayPort); err != nil {
		s.ensurePortPool().release(relayConn)
		releaseLane()
		releaseAdmit()
		return
	}

	c.SetDeadline(time.Time{})
	assocCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lc := &liveConn{cancel: cancel, info: protocol.LiveConn{
		ID: req.rec.ID, UserID: req.rec.UserID, Username: req.rec.Username, ClientIP: req.rec.ClientIP,
		LaneID: req.rec.LaneID, ExitIP: req.rec.ExitIP, Protocol: req.rec.Protocol, StartedAt: req.rec.StartedAt,
	}}
	s.mu.Lock()
	s.live[req.rec.ID] = lc
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.live, req.rec.ID)
		s.mu.Unlock()
	}()

	s.udpAssociations.Add(1)
	defer s.udpAssociations.Add(-1)

	var clientPort atomic.Uint32
	var clientPortSet bool
	var clientPortMu sync.Mutex
	idle := s.userIdleTimeout(u)
	var lastActive atomic.Int64
	lastActive.Store(time.Now().UnixNano())

	uid, lane := req.rec.UserID, req.rec.LaneID
	clientIP := req.client
	hostCache := newUDPHostCache()
	relayLog := s.Log

	copyToLane := func() {
		buf := make([]byte, 64*1024)
		for {
			n, addr, err := relayConn.ReadFromUDP(buf)
			if err != nil {
				cancel()
				return
			}
			s.udpPackets.Add(1)
			ip, port, ok := socks5.ClientUDPAddr(addr)
			if !ok || ip != clientIP {
				continue
			}
			clientPortMu.Lock()
			if !clientPortSet {
				clientPort.Store(uint32(port))
				clientPortSet = true
			} else if uint16(clientPort.Load()) != port {
				clientPortMu.Unlock()
				continue
			}
			clientPortMu.Unlock()

			h, payload, err := socks5.ParseUDP(buf[:n])
			if err != nil {
				s.logUDPDrop(relayLog, "parse", err)
				continue
			}
			lastActive.Store(time.Now().UnixNano())
			dstIP, err := resolveUDPAddr(assocCtx, s.Lanes, lane, h.Host, hostCache)
			if err != nil {
				s.logUDPDrop(relayLog, "resolve", err)
				continue
			}
			dst := &net.UDPAddr{IP: dstIP.AsSlice(), Port: int(h.Port)}
			if _, err := lanePC.WriteTo(payload, dst); err != nil {
				s.logUDPDrop(relayLog, "write", err)
				continue
			}
			lc.up.Add(int64(len(payload)))
			s.Policy.AddUsage(uid, lane, int64(len(payload)), 0)
		}
	}

	copyFromLane := func() {
		buf := make([]byte, 64*1024)
		for {
			n, addr, err := lanePC.ReadFrom(buf)
			if err != nil {
				cancel()
				return
			}
			s.udpPackets.Add(1)
			ua, ok := addr.(*net.UDPAddr)
			if !ok {
				continue
			}
			host := ua.IP.String()
			wrapped, err := socks5.BuildUDP(host, uint16(ua.Port), buf[:n])
			if err != nil {
				continue
			}
			lastActive.Store(time.Now().UnixNano())
			clientPortMu.Lock()
			cp := uint16(clientPort.Load())
			clientPortMu.Unlock()
			if cp == 0 {
				continue
			}
			_, err = relayConn.WriteToUDP(wrapped, &net.UDPAddr{IP: clientIP.AsSlice(), Port: int(cp)})
			if err != nil {
				cancel()
				return
			}
			lc.down.Add(int64(len(wrapped)))
			s.Policy.AddUsage(uid, lane, 0, int64(len(wrapped)))
		}
	}

	go copyToLane()
	go copyFromLane()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	done := make(chan struct{})
	go func() {
		io.Copy(io.Discard, c)
		close(done)
	}()

	result := protocol.ResultOK
	for {
		select {
		case <-done:
			goto cleanup
		case <-assocCtx.Done():
			goto cleanup
		case <-ticker.C:
			if lc.kicked.Load() {
				result = protocol.ResultKicked
				goto cleanup
			}
			if time.Since(time.Unix(0, lastActive.Load())) > idle {
				goto cleanup
			}
		}
	}

cleanup:
	cancel()
	s.ensurePortPool().release(relayConn)
	releaseLane()
	releaseAdmit()
	c.Close()
	req.rec.BytesUp, req.rec.BytesDown = lc.up.Load(), lc.down.Load()
	req.rec.EndedAt = time.Now()
	req.rec.Result = result
	if s.shouldLogConn(u) {
		s.record(req.rec)
	}
	s.Policy.CountConnection(uid, lane, result != protocol.ResultOK)
}

func socksAssociateReply(c net.Conn, host string, port int) error {
	if _, err := c.Write([]byte{0x05, socksOK, 0x00}); err != nil {
		return err
	}
	return socks5.WriteBindAddr(c, host, uint16(port))
}
