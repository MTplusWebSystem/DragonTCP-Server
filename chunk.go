package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"time"

	"dragontcp/internal/protocol"
	"dragontcp/internal/wire"
)

type streamSession struct {
	sid          wire.SessionID
	target       net.Conn
	targetName   string
	maxChunk     int
	maxBuffer    int
	debug        *serverDebug
	bulkCoalesce bool

	mu       sync.Mutex
	notify   chan struct{}
	buf      []byte
	base     uint64
	eof      bool
	closed   bool
	lastSeen time.Time

	upMu       sync.Mutex
	expectedUp uint64
}

func newStreamSession(sid wire.SessionID, target net.Conn, targetName string, maxChunk, maxBuffer int, bulkCoalesce bool, debug *serverDebug) *streamSession {
	s := &streamSession{
		sid:          sid,
		target:       target,
		targetName:   targetName,
		maxChunk:     maxChunk,
		maxBuffer:    maxBuffer,
		debug:        debug,
		bulkCoalesce: bulkCoalesce,
		notify:       make(chan struct{}),
		lastSeen:     time.Now(),
	}
	go s.readTarget()
	return s
}

func (s *streamSession) signalLocked() {
	close(s.notify)
	s.notify = make(chan struct{})
}

func (s *streamSession) touchLocked() { s.lastSeen = time.Now() }

func (s *streamSession) readTarget() {
	ptr := protocol.BufferPool.Get().(*[]byte)
	tmp := *ptr
	defer protocol.BufferPool.Put(ptr)
	for {
		n, err := s.target.Read(tmp)
		if n > 0 {
			data := tmp[:n]
			for len(data) > 0 {
				s.mu.Lock()
				for !s.closed && len(s.buf) >= s.maxBuffer {
					ch := s.notify
					s.mu.Unlock()
					<-ch
					s.mu.Lock()
				}
				if s.closed {
					s.mu.Unlock()
					return
				}
				room := s.maxBuffer - len(s.buf)
				take := len(data)
				if take > room {
					take = room
				}
				s.buf = append(s.buf, data[:take]...)
				data = data[take:]
				s.touchLocked()
				s.signalLocked()
				s.mu.Unlock()
				if s.debug != nil && s.debug.enabled {
					s.debug.bytesDown.Add(uint64(take))
				}
			}
		}
		if err != nil {
			s.mu.Lock()
			if !s.closed {
				s.eof = true
				s.touchLocked()
				s.signalLocked()
			}
			s.mu.Unlock()
			return
		}
	}
}

func (s *streamSession) ackLocked(offset uint64) {
	if offset <= s.base {
		return
	}
	end := s.base + uint64(len(s.buf))
	if offset > end {
		offset = end
	}
	drop := int(offset - s.base)
	if drop <= 0 {
		return
	}
	s.buf = s.buf[drop:]
	s.base = offset
	if len(s.buf) == 0 {
		s.buf = nil
	} else if cap(s.buf) > 4*len(s.buf) && cap(s.buf) > 1024*1024 {
		compact := append([]byte(nil), s.buf...)
		s.buf = compact
	}
	s.signalLocked()
}

func (s *streamSession) ack(offset uint64) {
	s.mu.Lock()
	s.ackLocked(offset)
	s.touchLocked()
	s.mu.Unlock()
}

func (s *streamSession) readAt(offset uint64, limit int, wait time.Duration) ([]byte, byte, error) {
	if limit < 1 || limit > s.maxChunk {
		return nil, wire.StatusError, fmt.Errorf("invalid download limit %d", limit)
	}
	deadline := time.Now().Add(wait)
	firstDataAt := time.Time{}

	for {
		s.mu.Lock()
		s.touchLocked()
		if offset < s.base {
			s.mu.Unlock()
			return nil, wire.StatusError, fmt.Errorf("download offset %d was already acknowledged (base=%d)", offset, s.base)
		}
		rel64 := offset - s.base
		if rel64 <= uint64(len(s.buf)) {
			rel := int(rel64)
			available := len(s.buf) - rel
			if available > 0 {
				if firstDataAt.IsZero() {
					firstDataAt = time.Now()
				}
				// SSH packetization naturally feeds this stream in ~tens-of-KiB
				// bursts. Returning the first burst turns a DragonTCP download into
				// one SSH packet per WAN RTT. Internal carrier sessions therefore
				// get a slightly wider coalescing window and can accumulate at least
				// 512 KiB before the pull response is emitted. Ordinary destinations
				// retain the original 2 ms latency-oriented behavior.
				coalesceDelay := 2 * time.Millisecond
				coalesceGoal := limit
				if s.bulkCoalesce {
					coalesceDelay = 25 * time.Millisecond
					coalesceGoal = 512 * 1024
					if coalesceGoal > limit {
						coalesceGoal = limit
					}
				}
				elapsed := time.Since(firstDataAt)
				if available < limit && available < coalesceGoal && !s.eof && wait > 0 && elapsed < coalesceDelay {
					ch := s.notify
					remaining := coalesceDelay - elapsed
					if untilDeadline := time.Until(deadline); untilDeadline < remaining {
						remaining = untilDeadline
					}
					s.mu.Unlock()
					if remaining > 0 {
						select {
						case <-ch:
						case <-time.After(remaining):
						}
					}
					continue
				}
				n := available
				if n > limit {
					n = limit
				}
				out := append([]byte(nil), s.buf[rel:rel+n]...)
				s.mu.Unlock()
				return out, wire.StatusData, nil
			}
			if s.eof || s.closed {
				s.mu.Unlock()
				return nil, wire.StatusEOF, nil
			}
		} else {
			s.mu.Unlock()
			return nil, wire.StatusError, fmt.Errorf("download offset %d is beyond buffered stream end %d", offset, s.base+uint64(len(s.buf)))
		}

		if wait <= 0 || time.Now().After(deadline) {
			s.mu.Unlock()
			return nil, wire.StatusWait, nil
		}
		ch := s.notify
		remaining := time.Until(deadline)
		s.mu.Unlock()
		select {
		case <-ch:
		case <-time.After(remaining):
			return nil, wire.StatusWait, nil
		}
	}
}

func (s *streamSession) upload(offset uint64, data []byte) error {
	if len(data) == 0 || len(data) > s.maxChunk {
		return fmt.Errorf("invalid upload size %d", len(data))
	}
	s.upMu.Lock()
	defer s.upMu.Unlock()

	if offset < s.expectedUp {
		// Idempotent retry after a lost ACK.
		if offset+uint64(len(data)) <= s.expectedUp {
			return nil
		}
		return fmt.Errorf("overlapping upload retry at %d", offset)
	}
	if offset != s.expectedUp {
		return fmt.Errorf("upload gap: got %d expected %d", offset, s.expectedUp)
	}
	if _, err := s.target.Write(data); err != nil {
		return err
	}
	s.expectedUp += uint64(len(data))
	s.mu.Lock()
	s.touchLocked()
	s.mu.Unlock()
	if s.debug != nil && s.debug.enabled {
		s.debug.bytesUp.Add(uint64(len(data)))
		s.debug.pushRecords.Add(1)
	}
	return nil
}

func (s *streamSession) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.signalLocked()
	s.mu.Unlock()
	_ = s.target.Close()
}

type streamManager struct {
	mu       sync.RWMutex
	sessions map[string]*streamSession
	timeout  time.Duration
	debug    *serverDebug
}

func sidKey(sid wire.SessionID) string { return string(sid[:]) }

func newStreamManager(timeout time.Duration, debug *serverDebug) *streamManager {
	m := &streamManager{sessions: make(map[string]*streamSession), timeout: timeout, debug: debug}
	go m.cleanupLoop()
	return m
}

func (m *streamManager) get(sid wire.SessionID) *streamSession {
	m.mu.RLock()
	s := m.sessions[sidKey(sid)]
	m.mu.RUnlock()
	return s
}

func (m *streamManager) addOrGet(sid wire.SessionID, s *streamSession) (*streamSession, bool) {
	key := sidKey(sid)
	m.mu.Lock()
	if old := m.sessions[key]; old != nil {
		m.mu.Unlock()
		s.close()
		return old, false
	}
	m.sessions[key] = s
	m.mu.Unlock()
	return s, true
}

func (m *streamManager) remove(sid wire.SessionID) {
	key := sidKey(sid)
	m.mu.Lock()
	s := m.sessions[key]
	delete(m.sessions, key)
	m.mu.Unlock()
	if s != nil {
		s.close()
		if m.debug != nil && m.debug.enabled {
			m.debug.sessionsClosed.Add(1)
			m.debug.activeSessions.Add(-1)
		}
	}
}

func (m *streamManager) count() int { m.mu.RLock(); n := len(m.sessions); m.mu.RUnlock(); return n }

func (m *streamManager) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-m.timeout)
		var stale []wire.SessionID
		m.mu.RLock()
		for _, s := range m.sessions {
			s.mu.Lock()
			last := s.lastSeen
			closed := s.closed
			sid := s.sid
			s.mu.Unlock()
			if closed || last.Before(cutoff) {
				stale = append(stale, sid)
			}
		}
		m.mu.RUnlock()
		for _, sid := range stale {
			m.remove(sid)
		}
	}
}

func parseProbe(payload []byte) (kind byte, value int, token string, err error) {
	if len(payload) < 11 || !bytes.Equal(payload[:4], wire.ProbeMagic[:]) {
		return 0, 0, "", fmt.Errorf("bad probe payload")
	}
	kind = payload[4]
	tl := int(binary.BigEndian.Uint16(payload[5:7]))
	value = int(binary.BigEndian.Uint32(payload[7:11]))
	if 11+tl > len(payload) {
		return 0, 0, "", fmt.Errorf("bad probe token length")
	}
	token = string(payload[11 : 11+tl])
	return
}

func probePattern(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte((i*31 + 17) & 0xff)
	}
	return out
}

func validateIperfUploadPayload(payload []byte, token string, candidate int) bool {
	base := 11 + len(token)
	wantLen := candidate
	if wantLen < base {
		wantLen = base
	}
	if len(payload) != wantLen {
		return false
	}
	for i := base; i < len(payload); i++ {
		if payload[i] != byte((i*31+17)&0xff) {
			return false
		}
	}
	return true
}

func parseOpen(payload []byte) (token, host string, port int, err error) {
	if len(payload) < 6 {
		return "", "", 0, fmt.Errorf("bad OPEN payload")
	}
	tl := int(binary.BigEndian.Uint16(payload[0:2]))
	hl := int(binary.BigEndian.Uint16(payload[2:4]))
	port = int(binary.BigEndian.Uint16(payload[4:6]))
	if port < 1 || 6+tl+hl != len(payload) {
		return "", "", 0, fmt.Errorf("bad OPEN lengths")
	}
	token = string(payload[6 : 6+tl])
	host = string(payload[6+tl:])
	if host == "" {
		return "", "", 0, fmt.Errorf("empty target host")
	}
	return
}

func processWireRequest(conn net.Conn, req wire.Request, token string, allowPrivate bool, cache *dnsCache, tcpBuffer int, manager *streamManager, maxChunk, maxBuffer int, pollWait time.Duration, debug *serverDebug) error {
	switch req.Mode {
	case wire.ModeProbe:
		kind, value, supplied, err := parseProbe(req.Payload)
		if err != nil {
			return wire.WriteResponse(conn, wire.StatusError, []byte(err.Error()))
		}
		if !tokenEqual(supplied, token) {
			return wire.WriteResponse(conn, wire.StatusError, []byte("authentication failed"))
		}
		switch kind {
		case wire.ProbeUpload:
			if len(req.Payload) > maxChunk {
				return wire.WriteResponse(conn, wire.StatusError, []byte("probe too large"))
			}
			return wire.WriteResponse(conn, wire.StatusOK, nil)
		case wire.ProbeDownload:
			if value < 1 || value > maxChunk {
				return wire.WriteResponse(conn, wire.StatusError, []byte("probe too large"))
			}
			return wire.WriteMaskedResponse(conn, wire.StatusData, probePattern(value), req.Session, wire.ModeProbe, req.Seq)
		case wire.ProbeKeepalive:
			return wire.WriteResponse(conn, wire.StatusOK, nil)
		case wire.ProbeBatch:
			count := value
			if count < 1 {
				count = 1
			}
			if count > 16 {
				count = 16
			}
			for i := 0; i < count; i++ {
				data := probePattern(32)
				if err := wire.WriteMaskedResponse(conn, wire.StatusData, data, req.Session, wire.ModeProbe, req.Seq+uint64(i)); err != nil {
					return err
				}
			}
			return nil
		case wire.ProbeIperfUpload:
			if value < 1 || value > maxChunk || len(req.Payload) > maxChunk {
				return wire.WriteResponse(conn, wire.StatusError, []byte("iperf upload chunk too large"))
			}
			if !validateIperfUploadPayload(req.Payload, supplied, value) {
				return wire.WriteResponse(conn, wire.StatusError, []byte("iperf upload validation failed"))
			}
			if debug != nil && debug.enabled {
				debug.logf("CALIBRATION fake_iperf=upload peer=%s chunk=%d bytes=%d seq=%d pollers=1 outstanding=1", conn.RemoteAddr(), value, len(req.Payload), req.Seq)
			}
			return wire.WriteResponse(conn, wire.StatusOK, nil)
		case wire.ProbeIperfDownload:
			if value < 1 || value > maxChunk {
				return wire.WriteResponse(conn, wire.StatusError, []byte("iperf download chunk too large"))
			}
			count := wire.ProbeBurstCount(value)
			if debug != nil && debug.enabled {
				debug.logf("CALIBRATION fake_iperf=download peer=%s chunk=%d records=%d bytes=%d pollers=1 outstanding=1", conn.RemoteAddr(), value, count, value*count)
			}
			data := probePattern(value)
			for i := 0; i < count; i++ {
				if err := wire.WriteMaskedResponse(conn, wire.StatusData, data, req.Session, wire.ModeProbe, req.Seq+uint64(i)); err != nil {
					return err
				}
			}
			return nil
		default:
			return wire.WriteResponse(conn, wire.StatusError, []byte("unknown probe kind"))
		}

	case wire.ModeOpen:
		supplied, host, port, err := parseOpen(req.Payload)
		if err != nil {
			return wire.WriteResponse(conn, wire.StatusError, []byte(err.Error()))
		}
		if !tokenEqual(supplied, token) {
			return wire.WriteResponse(conn, wire.StatusError, []byte("authentication failed"))
		}
		if old := manager.get(req.Session); old != nil {
			body := make([]byte, 4)
			binary.BigEndian.PutUint32(body, uint32(maxChunk))
			return wire.WriteMaskedResponse(conn, wire.StatusOK, body, req.Session, wire.ModeOpen, req.Seq)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		target, err := dialTarget(ctx, host, port, allowPrivate, cache, tcpBuffer)
		cancel()
		if err != nil {
			return wire.WriteResponse(conn, wire.StatusError, []byte(err.Error()))
		}
		_, internalCarrier := lookupInternalTarget(host, port)
		session := newStreamSession(req.Session, target, fmt.Sprintf("%s:%d", host, port), maxChunk, maxBuffer, internalCarrier, debug)
		_, created := manager.addOrGet(req.Session, session)
		if created && debug != nil && debug.enabled {
			debug.sessionsOpened.Add(1)
			debug.activeSessions.Add(1)
			debug.logf("SESSION OPEN sid=%x target=%s:%d active_sessions=%d", req.Session[:4], host, port, manager.count())
		}
		body := make([]byte, 4)
		binary.BigEndian.PutUint32(body, uint32(maxChunk))
		return wire.WriteMaskedResponse(conn, wire.StatusOK, body, req.Session, wire.ModeOpen, req.Seq)

	case wire.ModeUpload:
		s := manager.get(req.Session)
		if s == nil {
			return wire.WriteResponse(conn, wire.StatusError, []byte("unknown session"))
		}
		if len(req.Payload) > maxChunk {
			return wire.WriteResponse(conn, wire.StatusError, []byte("upload too large"))
		}
		if err := s.upload(req.Seq, req.Payload); err != nil {
			return wire.WriteResponse(conn, wire.StatusError, []byte(err.Error()))
		}
		return wire.WriteResponse(conn, wire.StatusOK, nil)

	case wire.ModeDownload:
		s := manager.get(req.Session)
		if s == nil {
			return wire.WriteResponse(conn, wire.StatusError, []byte("unknown session"))
		}
		if len(req.Payload) != 14 {
			return wire.WriteResponse(conn, wire.StatusError, []byte("bad download request"))
		}
		ack := binary.BigEndian.Uint64(req.Payload[0:8])
		limit := int(binary.BigEndian.Uint32(req.Payload[8:12]))
		count := int(binary.BigEndian.Uint16(req.Payload[12:14]))
		if limit < 1 {
			limit = 1
		}
		if limit > maxChunk {
			limit = maxChunk
		}
		if count < 1 {
			count = 1
		}
		if count > 256 {
			count = 256
		}
		s.ack(ack)
		offset := req.Seq
		if debug != nil && debug.enabled {
			debug.pullRequests.Add(1)
		}
		for i := 0; i < count; i++ {
			wait := time.Duration(0)
			if i == 0 {
				wait = pollWait
			}
			data, status, err := s.readAt(offset, limit, wait)
			if err != nil {
				return wire.WriteResponse(conn, wire.StatusError, []byte(err.Error()))
			}
			switch status {
			case wire.StatusData:
				if debug != nil && debug.enabled {
					debug.dataRecords.Add(1)
				}
				if err := wire.WriteMaskedResponse(conn, wire.StatusData, data, req.Session, wire.ModeDownload, offset); err != nil {
					return err
				}
				offset += uint64(len(data))
			case wire.StatusWait:
				if debug != nil && debug.enabled {
					debug.waitRecords.Add(1)
				}
				return wire.WriteResponse(conn, wire.StatusWait, nil)
			case wire.StatusEOF:
				return wire.WriteResponse(conn, wire.StatusEOF, nil)
			default:
				return wire.WriteResponse(conn, wire.StatusError, []byte("invalid session read status"))
			}
		}
		return nil

	case wire.ModeClose:
		manager.remove(req.Session)
		return wire.WriteResponse(conn, wire.StatusOK, nil)
	default:
		return wire.WriteResponse(conn, wire.StatusError, []byte("unknown mode"))
	}
}
