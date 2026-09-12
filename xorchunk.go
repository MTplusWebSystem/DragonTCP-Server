package main

// This file is the LiteVPN v4 XOR chunk server, carried over verbatim. It
// handles the legacy UP/OK + XOR 0xAD wire (COPEN / CPUSH / CPULL / CCLOSE and
// the TUNNEL stream commands) so one server accepts both wire formats. The
// binary record handler lives in chunk.go; nothing here is shared with it
// except the target dialler, DNS cache, token check and debug counters.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"dragontcp/internal/cover"
	"dragontcp/internal/protocol"
	"dragontcp/internal/wire"
)

type chunkSession struct {
	id        string
	target    net.Conn
	maxChunk  int
	maxBuffer int

	mu       sync.Mutex
	notify   chan struct{}
	chunks   map[uint64][]byte
	buffered int
	nextDown uint64
	eof      bool
	closed   bool
	lastSeen time.Time
	debug    *serverDebug

	upMu       sync.Mutex
	expectedUp uint64
	lastUpSeq  uint64
	lastUpLen  int
	haveLastUp bool
}

func newChunkSession(id string, target net.Conn, maxChunk, maxBuffer int, debug *serverDebug) *chunkSession {
	if maxBuffer < maxChunk {
		maxBuffer = maxChunk
	}
	readSize := maxChunk
	if readSize > 64*1024 {
		readSize = 64 * 1024
	}
	mapCapacity := maxBuffer / readSize
	if mapCapacity < 1 {
		mapCapacity = 1
	}
	if mapCapacity > 256 {
		mapCapacity = 256
	}
	s := &chunkSession{
		id:        id,
		target:    target,
		maxChunk:  maxChunk,
		maxBuffer: maxBuffer,
		notify:    make(chan struct{}),
		chunks:    make(map[uint64][]byte, mapCapacity),
		lastSeen:  time.Now(),
		debug:     debug,
	}
	go s.readTarget()
	return s
}

func (s *chunkSession) signalLocked() {
	close(s.notify)
	s.notify = make(chan struct{})
}

func (s *chunkSession) touchLocked() {
	s.lastSeen = time.Now()
}

func (s *chunkSession) touch() {
	s.mu.Lock()
	s.touchLocked()
	s.mu.Unlock()
}

func (s *chunkSession) readTarget() {
	ptr := protocol.BufferPool.Get().(*[]byte)
	buf := *ptr
	defer protocol.BufferPool.Put(ptr)
	if s.maxChunk < len(buf) {
		buf = buf[:s.maxChunk]
	}

	for {
		n, err := s.target.Read(buf)
		if n > 0 {
			data := append([]byte(nil), buf[:n]...)
			if s.debug != nil && s.debug.enabled {
				s.debug.bytesDown.Add(uint64(n))
			}

			for {
				s.mu.Lock()
				if s.closed {
					s.mu.Unlock()
					return
				}
				if s.buffered+len(data) <= s.maxBuffer {
					seq := s.nextDown
					s.nextDown++
					s.chunks[seq] = data
					s.buffered += len(data)
					s.touchLocked()
					s.signalLocked()
					s.mu.Unlock()
					break
				}
				ch := s.notify
				s.mu.Unlock()
				<-ch
			}
		}

		if err != nil {
			if s.debug != nil && s.debug.enabled {
				s.debug.logf("TARGET EOF session=%s err=%v", s.id, err)
			}
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

// push is idempotent for the most recently accepted sequence. This matters
// when the server receives a record but the tiny ACK is lost: the client can
// retry the same sequence at a smaller adaptive size without duplicating bytes
// in the target stream. The ACK reports the length that was actually accepted.
func (s *chunkSession) push(seq uint64, data []byte) (int, error) {
	s.upMu.Lock()
	defer s.upMu.Unlock()

	if len(data) == 0 || len(data) > s.maxChunk {
		return 0, fmt.Errorf("upload record size %d is invalid", len(data))
	}

	if s.haveLastUp && seq == s.lastUpSeq {
		s.touch()
		return s.lastUpLen, nil
	}

	if seq < s.expectedUp {
		return 0, fmt.Errorf("upload sequence %d is too old", seq)
	}
	if seq > s.expectedUp {
		return 0, fmt.Errorf("unexpected upload sequence %d, expected %d", seq, s.expectedUp)
	}

	if _, err := s.target.Write(data); err != nil {
		return 0, err
	}

	if s.debug != nil && s.debug.enabled {
		s.debug.bytesUp.Add(uint64(len(data)))
		s.debug.pushRecords.Add(1)
	}

	s.lastUpSeq = seq
	s.lastUpLen = len(data)
	s.haveLastUp = true
	s.expectedUp++
	s.touch()
	return len(data), nil
}

// pull returns at most limit bytes from the requested stored chunk, beginning
// at offset. The chunk sequence stays stable while the client retries smaller
// fragments, so a large queued chunk can always be recovered after an MTU-like
// failure without reopening the proxied destination connection.
func (s *chunkSession) pull(want uint64, ack int64, offset, limit int, wait time.Duration) (data []byte, total int, eof bool, final uint64, waitExpired bool, err error) {
	if offset < 0 || limit <= 0 || limit > s.maxChunk {
		return nil, 0, false, 0, false, fmt.Errorf("invalid pull offset/limit")
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()

	for {
		s.mu.Lock()
		s.touchLocked()

		if ack >= 0 {
			removed := false
			for seq := range s.chunks {
				if seq <= uint64(ack) {
					s.buffered -= len(s.chunks[seq])
					delete(s.chunks, seq)
					removed = true
				}
			}
			if removed {
				s.signalLocked()
			}
		}

		if chunk, ok := s.chunks[want]; ok {
			if offset >= len(chunk) {
				s.mu.Unlock()
				return nil, len(chunk), false, 0, false, fmt.Errorf("pull offset %d beyond chunk size %d", offset, len(chunk))
			}
			end := offset + limit
			if end > len(chunk) {
				end = len(chunk)
			}
			out := append([]byte(nil), chunk[offset:end]...)
			total = len(chunk)
			s.mu.Unlock()
			return out, total, false, 0, false, nil
		}

		if s.eof && want >= s.nextDown {
			final = s.nextDown
			s.mu.Unlock()
			return nil, 0, true, final, false, nil
		}

		if s.closed {
			final = s.nextDown
			s.mu.Unlock()
			return nil, 0, true, final, false, nil
		}

		ch := s.notify
		s.mu.Unlock()

		select {
		case <-ch:
			continue
		case <-timer.C:
			return nil, 0, false, 0, true, nil
		}
	}
}

func (s *chunkSession) close() {
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

type chunkManager struct {
	mu       sync.RWMutex
	sessions map[string]*chunkSession
	timeout  time.Duration
	debug    *serverDebug
}

func newChunkManager(timeout time.Duration, debug *serverDebug) *chunkManager {
	m := &chunkManager{
		sessions: make(map[string]*chunkSession),
		timeout:  timeout,
		debug:    debug,
	}
	go m.cleanupLoop()
	return m
}

func (m *chunkManager) get(id string) *chunkSession {
	m.mu.RLock()
	s := m.sessions[id]
	m.mu.RUnlock()
	return s
}

func (m *chunkManager) count() int {
	m.mu.RLock()
	n := len(m.sessions)
	m.mu.RUnlock()
	return n
}

func (m *chunkManager) add(id string, s *chunkSession) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.sessions[id]; exists {
		return fmt.Errorf("session already exists")
	}
	m.sessions[id] = s
	return nil
}

func (m *chunkManager) remove(id string) {
	m.mu.Lock()
	s := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if s != nil {
		s.close()
	}
}

func (m *chunkManager) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		cutoff := time.Now().Add(-m.timeout)
		var stale []string

		m.mu.RLock()
		for id, s := range m.sessions {
			s.mu.Lock()
			last := s.lastSeen
			closed := s.closed
			s.mu.Unlock()
			if closed || last.Before(cutoff) {
				stale = append(stale, id)
			}
		}
		m.mu.RUnlock()

		for _, id := range stale {
			if m.debug != nil && m.debug.enabled {
				m.debug.logf("SESSION timeout-close id=%s active_sessions=%d", id, m.count())
			}
			m.remove(id)
			if m.debug != nil && m.debug.enabled {
				m.debug.sessionsClosed.Add(1)
				m.debug.activeSessions.Add(-1)
			}
		}
	}
}

func decodeWireToken(token string) string {
	if token == "-" {
		return ""
	}
	return token
}

func isChunkCommand(payload []byte) bool {
	return bytes.HasPrefix(payload, []byte("CPROBE ")) ||
		bytes.HasPrefix(payload, []byte("CIPERFUP ")) ||
		bytes.HasPrefix(payload, []byte("CIPERFDW ")) ||
		bytes.HasPrefix(payload, []byte("COPEN ")) ||
		bytes.HasPrefix(payload, []byte("CPUSH ")) ||
		bytes.HasPrefix(payload, []byte("CPULL ")) ||
		bytes.HasPrefix(payload, []byte("CCLOSE "))
}

func processChunkCommand(
	conn net.Conn,
	requestID uint32,
	payload []byte,
	token string,
	allowPrivate bool,
	cache *dnsCache,
	tcpBuffer int,
	manager *chunkManager,
	maxChunk int,
	maxBufferedBytes int,
	pollWait time.Duration,
	debug *serverDebug,
) error {
	if bytes.HasPrefix(payload, []byte("CPROBE ")) {
		parts := strings.Fields(string(payload))
		if len(parts) != 2 {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR bad CPROBE"))
		}
		if !tokenEqual(decodeWireToken(parts[1]), token) {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR authentication failed"))
		}
		return protocol.WriteResponseFrame(conn, requestID, []byte("PROBEOK"))
	}

	if bytes.HasPrefix(payload, []byte("CIPERFUP ")) {
		parts := bytes.SplitN(payload, []byte(" "), 4)
		if len(parts) != 4 {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR bad CIPERFUP"))
		}
		if !tokenEqual(decodeWireToken(string(parts[1])), token) {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR authentication failed"))
		}
		size, err := strconv.Atoi(string(parts[2]))
		if err != nil || size < 1 || size > maxChunk {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR iperf upload chunk too large"))
		}
		data := parts[3]
		if len(data) != size || !bytes.Equal(data, probePattern(size)) {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR iperf upload validation failed"))
		}
		if debug != nil && debug.enabled {
			debug.logf("CALIBRATION fake_iperf=upload wire=x peer=%s chunk=%d bytes=%d pollers=1 outstanding=1", conn.RemoteAddr(), size, len(data))
		}
		return protocol.WriteResponseFrame(conn, requestID, []byte("IPERFOK"))
	}

	if bytes.HasPrefix(payload, []byte("CIPERFDW ")) {
		parts := strings.Fields(string(payload))
		if len(parts) != 3 {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR bad CIPERFDW"))
		}
		if !tokenEqual(decodeWireToken(parts[1]), token) {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR authentication failed"))
		}
		size, err := strconv.Atoi(parts[2])
		if err != nil || size < 1 || size > maxChunk {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR iperf download chunk too large"))
		}
		if debug != nil && debug.enabled {
			debug.logf("CALIBRATION fake_iperf=download wire=x peer=%s chunk=%d bytes=%d pollers=1 outstanding=1", conn.RemoteAddr(), size, size)
		}
		return protocol.WriteResponseFrame(conn, requestID, probePattern(size))
	}

	if bytes.HasPrefix(payload, []byte("COPEN ")) {
		parts := strings.Fields(string(payload))
		if len(parts) != 5 {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR bad COPEN"))
		}
		if !tokenEqual(decodeWireToken(parts[1]), token) {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR authentication failed"))
		}
		sid := parts[2]
		if len(sid) < 16 || len(sid) > 64 {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR invalid session id"))
		}
		host := parts[3]
		port, err := strconv.Atoi(parts[4])
		if err != nil || port < 1 || port > 65535 {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR invalid port"))
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		target, err := dialTarget(ctx, host, port, allowPrivate, cache, tcpBuffer)
		cancel()
		if err != nil {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR "+err.Error()))
		}

		session := newChunkSession(sid, target, maxChunk, maxBufferedBytes, debug)
		if err := manager.add(sid, session); err != nil {
			session.close()
			if debug != nil && debug.enabled {
				debug.errorf("COPEN session=%s target=%s:%d failed: %v", sid, host, port, err)
			}
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR "+err.Error()))
		}
		if debug != nil && debug.enabled {
			debug.sessionsOpened.Add(1)
			debug.activeSessions.Add(1)
			debug.logf("SESSION OPEN id=%s peer=%v target=%s:%d max_chunk=%d active_sessions=%d", sid, conn.RemoteAddr(), host, port, maxChunk, manager.count())
			debug.chunkf("COPEN id=%s target=%s:%d -> OPENED max=%d", sid, host, port, maxChunk)
		}
		return protocol.WriteResponseFrame(conn, requestID, []byte(fmt.Sprintf("OPENED %d", maxChunk)))
	}

	if bytes.HasPrefix(payload, []byte("CPUSH ")) {
		parts := bytes.SplitN(payload, []byte(" "), 5)
		if len(parts) != 5 {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR bad CPUSH"))
		}
		if !tokenEqual(decodeWireToken(string(parts[1])), token) {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR authentication failed"))
		}
		sid := string(parts[2])
		seq, err := strconv.ParseUint(string(parts[3]), 10, 64)
		if err != nil {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR invalid sequence"))
		}
		s := manager.get(sid)
		if s == nil {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR unknown session"))
		}
		accepted, err := s.push(seq, parts[4])
		if err != nil {
			if debug != nil && debug.enabled {
				debug.errorf("CPUSH id=%s seq=%d bytes=%d: %v", sid, seq, len(parts[4]), err)
			}
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR "+err.Error()))
		}
		if debug != nil {
			debug.chunkf("CPUSH id=%s seq=%d bytes=%d -> ACK accepted=%d", sid, seq, len(parts[4]), accepted)
		}
		return protocol.WriteResponseFrame(conn, requestID, []byte(fmt.Sprintf("ACK %d %d", seq, accepted)))
	}

	if bytes.HasPrefix(payload, []byte("CPULL ")) {
		parts := strings.Fields(string(payload))
		if len(parts) != 7 {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR bad CPULL"))
		}
		if !tokenEqual(decodeWireToken(parts[1]), token) {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR authentication failed"))
		}
		s := manager.get(parts[2])
		if s == nil {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR unknown session"))
		}
		ack, err := strconv.ParseInt(parts[3], 10, 64)
		if err != nil || ack < -1 {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR invalid ack"))
		}
		want, err := strconv.ParseUint(parts[4], 10, 64)
		if err != nil {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR invalid want"))
		}
		offset, err := strconv.Atoi(parts[5])
		if err != nil || offset < 0 {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR invalid offset"))
		}
		limit, err := strconv.Atoi(parts[6])
		if err != nil || limit < 1 {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR invalid limit"))
		}
		if limit > maxChunk {
			limit = maxChunk
		}
		if debug != nil && debug.enabled {
			debug.pullRequests.Add(1)
			debug.chunkf("CPULL id=%s ack=%d want=%d offset=%d limit=%d", parts[2], ack, want, offset, limit)
		}

		data, total, eof, final, waitExpired, err := s.pull(want, ack, offset, limit, pollWait)
		if err != nil {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR "+err.Error()))
		}
		if waitExpired {
			if debug != nil && debug.enabled {
				debug.waitRecords.Add(1)
				debug.chunkf("CPULL id=%s want=%d -> WAIT", parts[2], want)
			}
			return protocol.WriteResponseFrame(conn, requestID, []byte("WAIT"))
		}
		if eof {
			if debug != nil {
				debug.chunkf("CPULL id=%s want=%d -> EOF final=%d", parts[2], want, final)
			}
			return protocol.WriteResponseFrame(conn, requestID, []byte(fmt.Sprintf("EOF %d", final)))
		}

		if debug != nil && debug.enabled {
			debug.dataRecords.Add(1)
			debug.chunkf("DATA id=%s seq=%d offset=%d bytes=%d total=%d", parts[2], want, offset, len(data), total)
		}
		prefix := []byte(fmt.Sprintf("DATA %d %d %d ", want, offset, total))
		out := make([]byte, len(prefix)+len(data))
		copy(out, prefix)
		copy(out[len(prefix):], data)
		return protocol.WriteResponseFrame(conn, requestID, out)
	}

	if bytes.HasPrefix(payload, []byte("CCLOSE ")) {
		parts := strings.Fields(string(payload))
		if len(parts) != 3 {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR bad CCLOSE"))
		}
		if !tokenEqual(decodeWireToken(parts[1]), token) {
			return protocol.WriteResponseFrame(conn, requestID, []byte("ERR authentication failed"))
		}
		manager.remove(parts[2])
		if debug != nil && debug.enabled {
			debug.sessionsClosed.Add(1)
			debug.activeSessions.Add(-1)
			debug.logf("SESSION CLOSE id=%s peer=%v active_sessions=%d", parts[2], conn.RemoteAddr(), manager.count())
			debug.chunkf("CCLOSE id=%s -> CLOSED", parts[2])
		}
		return protocol.WriteResponseFrame(conn, requestID, []byte("CLOSED"))
	}

	return protocol.WriteResponseFrame(conn, requestID, []byte("ERR unknown chunk command"))
}

// ---------------------------------------------------------------------------
// Wire detection and the XOR connection loop.
// ---------------------------------------------------------------------------

// prefixedConn replays bytes already consumed for wire detection before falling
// through to the socket. Using a plain bufio.Reader would be wrong here: the
// TUNNEL path relays the raw connection, so anything the detector buffered
// beyond the magic would be lost.
type prefixedConn struct {
	net.Conn
	r          io.Reader
	headerMask byte
	cover      cover.Profile
	muxV2      bool
}

func (p *prefixedConn) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *prefixedConn) HeaderMask() byte            { return p.headerMask }
func (p *prefixedConn) CoverProfile() cover.Profile { return p.cover }
func (p *prefixedConn) ClearPayload() bool          { return p.cover.Clear }
func (p *prefixedConn) MuxV2() bool                 { return p.muxV2 || p.cover.MuxV2 }

func detectDirectMuxV2(r *bufio.Reader, mask byte) bool {
	b29, err := r.Peek(29)
	if err != nil {
		return false
	}
	mode := b29[0] ^ mask
	var sid wire.SessionID
	copy(sid[:], b29[1:17])
	seq := binary.BigEndian.Uint64(b29[17:25])

	switch mode {
	case wire.ModeProbe:
		b33, _ := r.Peek(33)
		if len(b33) >= 33 {
			candidateV1 := make([]byte, 4)
			copy(candidateV1, b33[29:33])
			wire.MaskInPlace(candidateV1, sid, mode, seq, false)
			if bytes.Equal(candidateV1, wire.ProbeMagic[:]) || bytes.Equal(b33[29:33], wire.ProbeMagic[:]) {
				return false
			}
			return true
		}

	case wire.ModeOpen:
		b39, _ := r.Peek(39)
		if len(b39) >= 39 {
			len1 := binary.BigEndian.Uint32(b39[25:29])
			p1 := make([]byte, 6)
			copy(p1, b39[29:35])
			wire.MaskInPlace(p1, sid, mode, seq, false)
			tl1 := binary.BigEndian.Uint16(p1[0:2])
			hl1 := binary.BigEndian.Uint16(p1[2:4])
			port1 := binary.BigEndian.Uint16(p1[4:6])
			if port1 >= 1 && 6+tl1+hl1 == uint16(len1) {
				return false
			}
			tl1c := binary.BigEndian.Uint16(b39[29:31])
			hl1c := binary.BigEndian.Uint16(b39[31:33])
			port1c := binary.BigEndian.Uint16(b39[33:35])
			if port1c >= 1 && 6+tl1c+hl1c == uint16(len1) {
				return false
			}

			len2 := binary.BigEndian.Uint32(b39[29:33])
			p2 := make([]byte, 6)
			copy(p2, b39[33:39])
			wire.MaskInPlace(p2, sid, mode, seq, false)
			tl2 := binary.BigEndian.Uint16(p2[0:2])
			hl2 := binary.BigEndian.Uint16(p2[2:4])
			port2 := binary.BigEndian.Uint16(p2[4:6])
			if port2 >= 1 && 6+tl2+hl2 == uint16(len2) {
				return true
			}
			tl2c := binary.BigEndian.Uint16(b39[33:35])
			hl2c := binary.BigEndian.Uint16(b39[35:37])
			port2c := binary.BigEndian.Uint16(b39[37:39])
			if port2c >= 1 && 6+tl2c+hl2c == uint16(len2) {
				return true
			}
		}

	case wire.ModeDownload:
		len1 := binary.BigEndian.Uint32(b29[25:29])
		b33, _ := r.Peek(33)
		if len(b33) >= 33 {
			len2 := binary.BigEndian.Uint32(b33[29:33])
			if len1 == 14 && len2 != 14 {
				return false
			}
			if len2 == 14 && len1 != 14 {
				return true
			}
		}
	}
	return false
}

// sniffWire first checks for the optional self-describing cover preface. If it
// is absent, the bytes are replayed and the legacy/direct B/X classifier is
// used unchanged.
func sniffWire(conn net.Conn) (net.Conn, bool, byte, error) {
	var initial [cover.PrefaceSize]byte
	if _, err := io.ReadFull(conn, initial[:]); err != nil {
		return conn, false, 0, err
	}
	if profile, ok := cover.DecodePreface(initial); ok {
		if profile.Padding > 0 {
			padding := make([]byte, int(profile.Padding))
			if _, err := io.ReadFull(conn, padding); err != nil {
				return conn, false, 0, err
			}
		}
		profiled := &prefixedConn{Conn: conn, r: conn, headerMask: profile.HeaderMask, cover: profile, muxV2: profile.MuxV2}
		return profiled, profile.XOR, profile.HeaderMask, nil
	}

	magic := initial[:2]

	if magic[0]&7 >= 5 {
		mask := magic[0] ^ 'U'
		if magic[1]^mask != 'P' {
			return conn, false, 0, fmt.Errorf("unknown wire header")
		}
		replay := io.MultiReader(bytes.NewReader(initial[:]), conn)
		replayed := &prefixedConn{Conn: conn, r: replay, headerMask: mask}
		return replayed, true, mask, nil
	}

	mask := magic[0] & 0xf8
	mode := magic[0] ^ mask
	if mode > 4 {
		return conn, false, 0, fmt.Errorf("unknown binary mode")
	}

	reader := bufio.NewReader(io.MultiReader(bytes.NewReader(initial[:]), conn))
	isMux := detectDirectMuxV2(reader, mask)
	replayed := &prefixedConn{Conn: conn, r: reader, headerMask: mask, muxV2: isMux}
	return replayed, false, mask, nil
}

// handleXOR serves one connection speaking UP/OK + XOR 0xAD: the v4 chunk
// commands, plus the TUNNEL/TUNNEL2 stream commands.
func handleXOR(
	conn net.Conn,
	headerMask byte,
	token string,
	allowPrivate bool,
	cache *dnsCache,
	tcpBuffer int,
	manager *chunkManager,
	chunkMax int,
	bufferBytes int,
	chunkPollWait time.Duration,
	debug *serverDebug,
) {
	deadline := newIdleDeadline(conn, 20*time.Second)
	for {
		if deadline.refresh() != nil {
			return
		}

		requestID, _, payload, err := protocol.ReadRequestFrameProfile(conn, headerMask)
		if err != nil {
			if debug != nil && debug.enabled && err != io.EOF {
				debug.errorf("peer=%v read XOR request: %v", conn.RemoteAddr(), err)
			}
			return
		}

		if isChunkCommand(payload) {
			if err := processChunkCommand(
				conn, requestID, payload, token, allowPrivate, cache, tcpBuffer,
				manager, chunkMax, bufferBytes, chunkPollWait, debug,
			); err != nil {
				return
			}
			continue
		}

		parts := strings.Fields(string(payload))
		transport := "xor"
		if len(parts) == 4 && parts[0] == "TUNNEL" {
			transport = "xor"
		} else if len(parts) == 5 && parts[0] == "TUNNEL2" {
			transport = strings.ToLower(parts[4])
			if transport != "raw" && transport != "xor" {
				_ = protocol.WriteResponseFrame(conn, requestID, []byte("ERR transport must be RAW or XOR"))
				return
			}
		} else {
			_ = protocol.WriteResponseFrame(conn, requestID, []byte("ERR expected TUNNEL, TUNNEL2, or chunk command"))
			return
		}

		if !tokenEqual(parts[1], token) {
			_ = protocol.WriteResponseFrame(conn, requestID, []byte("ERR authentication failed"))
			return
		}

		port, err := strconv.Atoi(parts[3])
		if err != nil || port < 1 || port > 65535 {
			_ = protocol.WriteResponseFrame(conn, requestID, []byte("ERR invalid port"))
			return
		}

		if debug != nil && debug.enabled {
			debug.logf("TUNNEL peer=%v target=%s:%d transport=%s", conn.RemoteAddr(), parts[2], port, transport)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		target, err := dialTarget(ctx, parts[2], port, allowPrivate, cache, tcpBuffer)
		cancel()
		if err != nil {
			if debug != nil && debug.enabled {
				debug.errorf("TUNNEL target=%s:%d connect failed: %v", parts[2], port, err)
			}
			_ = protocol.WriteResponseFrame(conn, requestID, []byte("ERR "+err.Error()))
			return
		}
		defer target.Close()

		if err := protocol.WriteResponseFrame(conn, requestID, []byte("CONNECTED")); err != nil {
			return
		}

		_ = conn.SetDeadline(time.Time{})
		if transport == "raw" {
			protocol.RelayRaw(conn, target)
		} else {
			protocol.RelayXOR(conn, target)
		}
		if debug != nil && debug.enabled {
			debug.logf("TUNNEL closed peer=%v target=%s:%d transport=%s", conn.RemoteAddr(), parts[2], port, transport)
		}
		return
	}
}
