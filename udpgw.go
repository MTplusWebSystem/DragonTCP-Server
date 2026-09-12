package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"
)

// ── Config ────────────────────────────────────────────────────────────────────

type udpgwServerConfig struct {
	Listen         string
	MaxFrame       int
	MaxClients     int
	MaxClientConns int
	MaxMapEntries  int
	MapTTL         time.Duration
	IdleTimeout    time.Duration
	Mode           string // "native" (ABI Linux), "tun" (/dev/net/tun), "standard" (UDPGW padrão)
	Interface      string // "auto" ou interface física, ex: "eth0"
	BusyPollUS     int    // SO_BUSY_POLL em microssegundos (padrão: 50)
	Debug          bool
}

// ── Thread-safe mapping ───────────────────────────────────────────────────────

type udpDestKey struct {
	ip   [4]byte
	port uint16
}

type udpMapVal struct {
	connID uint16
	x      byte
	exp    time.Time
}

// udpMappings is a concurrent-safe store of udpDestKey → udpMapVal.
// It is shared between the main read loop (writer) and the receive goroutines (readers).
type udpMappings struct {
	mu   sync.RWMutex
	data map[udpDestKey]udpMapVal
}

func newUDPMappings() *udpMappings {
	return &udpMappings{data: make(map[udpDestKey]udpMapVal)}
}

func (m *udpMappings) get(k udpDestKey) (udpMapVal, bool) {
	m.mu.RLock()
	v, ok := m.data[k]
	m.mu.RUnlock()
	if ok && time.Now().After(v.exp) {
		return udpMapVal{}, false
	}
	return v, ok
}

func (m *udpMappings) set(k udpDestKey, v udpMapVal) {
	m.mu.Lock()
	m.data[k] = v
	m.mu.Unlock()
}

func (m *udpMappings) delete(k udpDestKey) {
	m.mu.Lock()
	delete(m.data, k)
	m.mu.Unlock()
}

func (m *udpMappings) reap(now time.Time) {
	m.mu.Lock()
	for k, v := range m.data {
		if now.After(v.exp) {
			delete(m.data, k)
		}
	}
	m.mu.Unlock()
}

func (m *udpMappings) len() int {
	m.mu.RLock()
	n := len(m.data)
	m.mu.RUnlock()
	return n
}

// evictOne removes one arbitrary entry (used when map is full).
func (m *udpMappings) evictOne() {
	m.mu.Lock()
	for k := range m.data {
		delete(m.data, k)
		break
	}
	m.mu.Unlock()
}

// ── Server ────────────────────────────────────────────────────────────────────

type udpgwServer struct {
	cfg     udpgwServerConfig
	ln      net.Listener
	tunDev  *tunDevice
	slots   chan struct{}
	closeMu sync.Once
}

func startUDPGWServer(cfg udpgwServerConfig) (*udpgwServer, error) {
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:7400"
	}
	if cfg.MaxFrame <= 0 || cfg.MaxFrame > 65535 {
		cfg.MaxFrame = 65535
	}
	if cfg.MaxClients <= 0 {
		cfg.MaxClients = 10000
	}
	if cfg.MaxClientConns <= 0 {
		cfg.MaxClientConns = 64
	}
	if cfg.MaxMapEntries <= 0 {
		cfg.MaxMapEntries = 32768
	}
	if cfg.MapTTL <= 0 {
		cfg.MapTTL = 90 * time.Second
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 2 * time.Minute
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	switch mode {
	case "native", "abi", "linux":
		mode = "native"
	case "tun":
		mode = "tun"
	default:
		mode = "standard"
	}
	cfg.Mode = mode
	if cfg.Interface == "" {
		cfg.Interface = "auto"
	}
	if cfg.BusyPollUS <= 0 {
		cfg.BusyPollUS = 50
	}

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return nil, err
	}
	s := &udpgwServer{cfg: cfg, ln: ln, slots: make(chan struct{}, cfg.MaxClients)}
	if cfg.Mode == "tun" {
		tunDev, err := openTunDevice("")
		if err != nil {
			if cfg.Debug {
				log.Printf("udpgw tun mode unavailable, falling back to native ABI: %v", err)
			}
			s.cfg.Mode = "native"
		} else {
			s.tunDev = tunDev
			log.Printf("udpgw tun mode active: iface=%s", tunDev.name)
		}
	}
	go s.acceptLoop()
	log.Printf("udpgw listening on %s (mode=%s)", cfg.Listen, s.cfg.Mode)
	return s, nil
}

func (s *udpgwServer) Close() error {
	var err error
	s.closeMu.Do(func() {
		err = s.ln.Close()
		if s.tunDev != nil {
			_ = s.tunDev.Close()
		}
	})
	return err
}

func (s *udpgwServer) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			log.Printf("udpgw accept: %v", err)
			continue
		}
		select {
		case s.slots <- struct{}{}:
			go func() {
				defer func() { <-s.slots }()
				s.handleClient(conn)
			}()
		default:
			_ = conn.Close()
		}
	}
}

// ── handleClient ──────────────────────────────────────────────────────────────

func (s *udpgwServer) handleClient(conn net.Conn) {
	defer conn.Close()
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}

	// ── UDP socket for standard & native modes ────────────────────────────────
	var udpConn *net.UDPConn
	if s.cfg.Mode != "tun" {
		var err error
		udpConn, err = net.ListenUDP("udp4", nil)
		if err != nil {
			return
		}
		defer udpConn.Close()

		if s.cfg.Mode == "native" {
			boundIface, err := setupNativeUDPSocket(udpConn, s.cfg.Interface, s.cfg.BusyPollUS)
			if s.cfg.Debug {
				if err != nil {
					log.Printf("udpgw native abi warning: %v (bound=%s)", err, boundIface)
				} else {
					log.Printf("udpgw native abi active: bound_iface=%s busy_poll=%dµs", boundIface, s.cfg.BusyPollUS)
				}
			}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	writeCh := make(chan []byte, 256)
	done := make(chan struct{})

	// ── TCP writer goroutine ──────────────────────────────────────────────────
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case frame := <-writeCh:
				_ = conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
				if _, err := conn.Write(frame); err != nil {
					cancel()
					_ = conn.Close()
					return
				}
			}
		}
	}()

	// ── Shared mapping table ──────────────────────────────────────────────────
	mappings := newUDPMappings()
	connSeen := make(map[uint16]time.Time)
	var connSeenMu sync.Mutex

	// ── UDP → TCP receive goroutine (mode-aware) ──────────────────────────────
	switch s.cfg.Mode {
	case "tun":
		go readTunUDP(s.tunDev, mappings, writeCh, done)
	case "native":
		go readNativeUDP(udpConn, mappings, writeCh, done)
	default:
		go readStandardUDP(udpConn, mappings, writeCh, done)
	}

	// ── Map reaper ────────────────────────────────────────────────────────────
	reap := time.NewTicker(10 * time.Second)
	defer reap.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-reap.C:
				mappings.reap(now)
				connSeenMu.Lock()
				for id, seen := range connSeen {
					if now.Sub(seen) > s.cfg.MapTTL {
						delete(connSeen, id)
					}
				}
				connSeenMu.Unlock()
			}
		}
	}()

	// ── Main TCP read loop (TCP → UDP) ────────────────────────────────────────
	br := bufio.NewReaderSize(conn, 32*1024)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(s.cfg.IdleTimeout))
		payload, err := udpgwReadPayload(br, s.cfg.MaxFrame)
		if err != nil {
			cancel()
			_ = conn.Close()
			<-done
			return
		}
		if len(payload) < 9 {
			continue
		}
		connID := binary.BigEndian.Uint16(payload[0:2])
		x := payload[2]
		var dstIP [4]byte
		copy(dstIP[:], payload[3:7])
		dstPort := binary.BigEndian.Uint16(payload[7:9])
		data := payload[9:]
		now := time.Now()
		key := udpDestKey{ip: dstIP, port: dstPort}

		// Track connID → last seen
		connSeenMu.Lock()
		for id, seen := range connSeen {
			if now.Sub(seen) > s.cfg.MapTTL {
				delete(connSeen, id)
			}
		}
		if _, exists := connSeen[connID]; !exists && len(connSeen) >= s.cfg.MaxClientConns {
			var oldestID uint16
			var oldestTime time.Time
			first := true
			for id, seen := range connSeen {
				if first || seen.Before(oldestTime) {
					oldestID, oldestTime, first = id, seen, false
				}
			}
			delete(connSeen, oldestID)
		}
		connSeen[connID] = now
		connSeenMu.Unlock()

		// Maintain mapping table size
		if mappings.len() >= s.cfg.MaxMapEntries {
			mappings.reap(now)
			if mappings.len() >= s.cfg.MaxMapEntries {
				mappings.evictOne()
			}
		}
		mappings.set(key, udpMapVal{connID: connID, x: x, exp: now.Add(s.cfg.MapTTL)})

		// ── Send packet (mode-aware) ──────────────────────────────────────────
		switch s.cfg.Mode {
		case "tun":
			// For TUN mode we inject a raw IPv4+UDP packet into the kernel TUN interface.
			// We use 0.0.0.0 as the fake source; the kernel routes the reply back via TUN.
			var srcIP [4]byte // 0.0.0.0 — kernel fills routing
			writeTunUDP(s.tunDev, srcIP, 0, dstIP, dstPort, data)
		default:
			addr := &net.UDPAddr{IP: net.IPv4(dstIP[0], dstIP[1], dstIP[2], dstIP[3]), Port: int(dstPort)}
			if _, err := udpConn.WriteToUDP(data, addr); err != nil && s.cfg.Debug {
				log.Printf("udpgw write %s: %v", addr, err)
			}
		}
	}
}

// ── Wire helpers ──────────────────────────────────────────────────────────────

func udpgwReadPayload(r *bufio.Reader, max int) ([]byte, error) {
	var lenBuf [2]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, err
	}
	n := int(binary.LittleEndian.Uint16(lenBuf[:]))
	if n <= 0 || n > max {
		return nil, fmt.Errorf("udpgw invalid frame length %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func udpgwBuildFrame(connID uint16, x byte, ip [4]byte, port uint16, data []byte) []byte {
	payloadLen := 9 + len(data)
	out := make([]byte, 2+payloadLen)
	binary.LittleEndian.PutUint16(out[0:2], uint16(payloadLen))
	binary.BigEndian.PutUint16(out[2:4], connID)
	out[4] = x
	copy(out[5:9], ip[:])
	binary.BigEndian.PutUint16(out[9:11], port)
	copy(out[11:], data)
	return out
}
