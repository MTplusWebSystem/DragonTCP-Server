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
	"sync"
	"time"
)

type udpgwServerConfig struct {
	Listen         string
	MaxFrame       int
	MaxClients     int
	MaxClientConns int
	MaxMapEntries  int
	MapTTL         time.Duration
	IdleTimeout    time.Duration
	Debug          bool
}

type udpgwServer struct {
	cfg     udpgwServerConfig
	ln      net.Listener
	slots   chan struct{}
	closeMu sync.Once
}

type udpDestKey struct {
	ip   [4]byte
	port uint16
}

type udpMapVal struct {
	connID uint16
	x      byte
	exp    time.Time
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
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return nil, err
	}
	s := &udpgwServer{cfg: cfg, ln: ln, slots: make(chan struct{}, cfg.MaxClients)}
	go s.acceptLoop()
	return s, nil
}

func (s *udpgwServer) Close() error {
	var err error
	s.closeMu.Do(func() { err = s.ln.Close() })
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

func (s *udpgwServer) handleClient(conn net.Conn) {
	defer conn.Close()
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}
	udpConn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return
	}
	defer udpConn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writeCh := make(chan []byte, 256)
	done := make(chan struct{})
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

	var mu sync.Mutex
	mappings := make(map[udpDestKey]udpMapVal)
	connSeen := make(map[uint16]time.Time)

	go func() {
		buf := make([]byte, 65535)
		for {
			n, from, err := udpConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			ip4 := from.IP.To4()
			if ip4 == nil || n <= 0 {
				continue
			}
			var ip [4]byte
			copy(ip[:], ip4)
			key := udpDestKey{ip: ip, port: uint16(from.Port)}
			mu.Lock()
			v, ok := mappings[key]
			mu.Unlock()
			if !ok || time.Now().After(v.exp) {
				continue
			}
			frame := udpgwBuildFrame(v.connID, v.x, ip, uint16(from.Port), buf[:n])
			select {
			case writeCh <- frame:
			default:
			}
		}
	}()

	reap := time.NewTicker(10 * time.Second)
	defer reap.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-reap.C:
				mu.Lock()
				for k, v := range mappings {
					if now.After(v.exp) {
						delete(mappings, k)
					}
				}
				for id, seen := range connSeen {
					if now.Sub(seen) > s.cfg.MapTTL {
						delete(connSeen, id)
					}
				}
				mu.Unlock()
			}
		}
	}()

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

		mu.Lock()
		for id, seen := range connSeen {
			if now.Sub(seen) > s.cfg.MapTTL {
				delete(connSeen, id)
			}
		}
		if _, ok := connSeen[connID]; !ok && len(connSeen) >= s.cfg.MaxClientConns {
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
		if len(mappings) >= s.cfg.MaxMapEntries {
			for k, v := range mappings {
				if now.After(v.exp) {
					delete(mappings, k)
				}
			}
			if len(mappings) >= s.cfg.MaxMapEntries {
				for k := range mappings {
					delete(mappings, k)
					break
				}
			}
		}
		mappings[key] = udpMapVal{connID: connID, x: x, exp: now.Add(s.cfg.MapTTL)}
		mu.Unlock()

		addr := &net.UDPAddr{IP: net.IPv4(dstIP[0], dstIP[1], dstIP[2], dstIP[3]), Port: int(dstPort)}
		if _, err := udpConn.WriteToUDP(data, addr); err != nil && s.cfg.Debug {
			log.Printf("udpgw write %s: %v", addr, err)
		}
	}
}

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
