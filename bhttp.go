package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"dragontcp/internal/wire"
)

const (
	bhttpModeProbe         byte = 0
	bhttpModeUpload        byte = 1
	bhttpModeDownload      byte = 2
	bhttpModeBatchDownload byte = 3
	bhttpModeACK           byte = 4
	bhttpProbeVersion      byte = 1
	bhttpRequestHeaderSize      = 29
)

var bhttpProbeMagic = [4]byte{'B', 'H', 'P', '1'}
var bhttpOpenMagic = [4]byte{'D', 'O', 'P', '1'}
var bpCloseMagic = [4]byte{'D', 'C', 'L', '1'}

// bhttpSession intentionally models only the transport/session behavior that
// is observable in bhttp_remote_test.py. The supplied client test contains no
// destination-selection handshake, so uploads are acknowledged and counted but
// are not forwarded to an invented target.
type bhttpSession struct {
	mu       sync.Mutex
	lastSeen time.Time
	uploaded uint64
	acked    uint64
	stream   *streamSession
}

func (s *bhttpSession) touch() {
	s.mu.Lock()
	s.lastSeen = time.Now()
	s.mu.Unlock()
}

type bhttpSessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*bhttpSession
	timeout  time.Duration
	max      int
}

func newBHTTPSessionManager(timeout time.Duration, max int) *bhttpSessionManager {
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	if max < 1 {
		max = 1
	}
	m := &bhttpSessionManager{
		sessions: make(map[string]*bhttpSession),
		timeout:  timeout,
		max:      max,
	}
	go m.cleanupLoop()
	return m
}

func (m *bhttpSessionManager) get(sid wire.SessionID) *bhttpSession {
	m.mu.RLock()
	s := m.sessions[sidKey(sid)]
	m.mu.RUnlock()
	if s != nil {
		s.touch()
	}
	return s
}

func (m *bhttpSessionManager) register(sid wire.SessionID) bool {
	key := sidKey(sid)
	m.mu.Lock()
	if old := m.sessions[key]; old != nil {
		m.mu.Unlock()
		old.touch()
		return true
	}
	if len(m.sessions) >= m.max {
		m.mu.Unlock()
		return false
	}
	m.sessions[key] = &bhttpSession{lastSeen: time.Now()}
	m.mu.Unlock()
	return true
}

func (m *bhttpSessionManager) remove(sid wire.SessionID) bool {
	key := sidKey(sid)
	m.mu.Lock()
	session := m.sessions[key]
	delete(m.sessions, key)
	m.mu.Unlock()
	if session == nil {
		return false
	}
	session.mu.Lock()
	stream := session.stream
	session.stream = nil
	session.mu.Unlock()
	if stream != nil {
		stream.close()
	}
	return true
}

func (m *bhttpSessionManager) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for now := range ticker.C {
		cutoff := now.Add(-m.timeout)
		var closing []*streamSession
		m.mu.Lock()
		for key, session := range m.sessions {
			session.mu.Lock()
			stale := session.lastSeen.Before(cutoff)
			stream := session.stream
			session.mu.Unlock()
			if stale {
				delete(m.sessions, key)
				if stream != nil {
					closing = append(closing, stream)
				}
			}
		}
		m.mu.Unlock()
		for _, stream := range closing {
			stream.close()
		}
	}
}

type bhttpRequest struct {
	mode       byte
	session    wire.SessionID
	seq        uint64
	value      uint32
	payload    []byte
	headerMask byte
	clear      bool
}

type binaryHeader struct {
	mode    byte
	session wire.SessionID
	seq     uint64
	length  uint32
}

func peekBinaryHeader(r *bufio.Reader, headerMask byte) (binaryHeader, error) {
	var out binaryHeader
	header, err := r.Peek(bhttpRequestHeaderSize)
	if err != nil {
		return out, err
	}
	out.mode = header[0] ^ headerMask
	copy(out.session[:], header[1:17])
	out.seq = binary.BigEndian.Uint64(header[17:25])
	out.length = binary.BigEndian.Uint32(header[25:29])
	return out, nil
}

func readBHTTPRequest(r *bufio.Reader, headerMask byte, clear bool) (bhttpRequest, error) {
	var req bhttpRequest
	var header [bhttpRequestHeaderSize]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return req, err
	}
	req.mode = header[0] ^ headerMask
	req.headerMask = headerMask
	req.clear = clear
	if req.mode > bhttpModeACK {
		return req, fmt.Errorf("unknown BP mode")
	}
	copy(req.session[:], header[1:17])
	req.seq = binary.BigEndian.Uint64(header[17:25])
	req.value = binary.BigEndian.Uint32(header[25:29])

	// BHTTP mode 2 overloads the normal body-length field as a download-size
	// hint and sends no payload bytes after the 29-byte header.
	if req.mode == bhttpModeDownload {
		return req, nil
	}
	if req.value > wire.MaxPayload {
		return req, fmt.Errorf("BP payload too large")
	}
	if req.value > 0 {
		req.payload = make([]byte, int(req.value))
		if _, err := io.ReadFull(r, req.payload); err != nil {
			return req, err
		}
		if !clear {
			wire.MaskInPlace(req.payload, req.session, req.mode, req.seq, false)
		}
	}
	return req, nil
}

func parseBHTTPProbe(payload []byte) (byte, int, error) {
	if len(payload) < 10 || !bytes.Equal(payload[:4], bhttpProbeMagic[:]) || payload[4] != bhttpProbeVersion {
		return 0, 0, fmt.Errorf("bad BP probe")
	}
	submode := payload[5]
	if submode > bhttpModeACK {
		return 0, 0, fmt.Errorf("unknown BP probe submode")
	}
	param := int(binary.BigEndian.Uint32(payload[6:10]))
	want := 10
	if submode == bhttpModeUpload && param >= 10 {
		want = param
	}
	if len(payload) != want {
		return 0, 0, fmt.Errorf("bad BP probe length")
	}
	for i := 10; i < len(payload); i++ {
		if payload[i] != byte(i*31) {
			return 0, 0, fmt.Errorf("bad BP probe pattern")
		}
	}
	return submode, param, nil
}

func makeBHTTPProbe(submode byte, param int) []byte {
	total := 10
	if submode == bhttpModeDownload && param > total {
		total = param
	}
	out := make([]byte, total)
	copy(out[:4], bhttpProbeMagic[:])
	out[4] = bhttpProbeVersion
	out[5] = submode
	binary.BigEndian.PutUint32(out[6:10], uint32(param))
	for i := 10; i < len(out); i++ {
		out[i] = byte(i * 31)
	}
	return out
}

func writeBHTTPError(conn net.Conn, message string) error {
	return wire.WriteResponse(conn, wire.StatusError, []byte(message))
}

func writeBHTTPMasked(conn net.Conn, status byte, body []byte, req bhttpRequest) error {
	return wire.WriteMaskedResponseProfileEncoding(conn, status, body, req.session, req.mode, req.seq, req.headerMask, req.clear)
}

func writeBHTTPData(conn net.Conn, req bhttpRequest, data []byte) error {
	// Build and mask the complete response once. The generic two-step path
	// first built a BP body and then copied it into another framed packet,
	// temporarily allocating roughly twice the download size.
	if req.clear {
		var header [wire.ResponseHeaderSize]byte
		header[0] = wire.StatusData ^ req.headerMask
		binary.BigEndian.PutUint32(header[1:5], uint32(4+len(data)))
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(data)))
		buffers := net.Buffers{header[:], length[:], data}
		_, err := buffers.WriteTo(conn)
		return err
	}
	packet := make([]byte, wire.ResponseHeaderSize+4+len(data))
	packet[0] = wire.StatusData ^ req.headerMask
	binary.BigEndian.PutUint32(packet[1:5], uint32(4+len(data)))
	binary.BigEndian.PutUint32(packet[5:9], uint32(len(data)))
	copy(packet[9:], data)
	wire.MaskInPlace(packet[5:], req.session, req.mode, req.seq, true)
	for len(packet) > 0 {
		n, err := conn.Write(packet)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		packet = packet[n:]
	}
	return nil
}

type bhttpServerContext struct {
	sessions     *bhttpSessionManager
	token        string
	allowPrivate bool
	cache        *dnsCache
	tcpBuffer    int
	maxChunk     int
	maxBuffer    int
	pollWait     time.Duration
	debug        *serverDebug
}

func processBHTTPRequest(conn net.Conn, req bhttpRequest, ctx *bhttpServerContext) error {
	sessions := ctx.sessions
	maxChunk := ctx.maxChunk
	switch req.mode {
	case bhttpModeProbe:
		submode, param, err := parseBHTTPProbe(req.payload)
		if err != nil {
			return writeBHTTPError(conn, err.Error())
		}
		if submode == bhttpModeUpload && len(req.payload) > maxChunk {
			return writeBHTTPError(conn, "probe too large")
		}
		if submode == bhttpModeDownload && (param < 0 || param > maxChunk) {
			return writeBHTTPError(conn, "probe too large")
		}
		count := 1
		if submode == bhttpModeACK {
			count = param
			if count < 1 {
				count = 1
			}
			if count > 256 {
				count = 256
			}
		}
		body := makeBHTTPProbe(submode, param)
		for i := 0; i < count; i++ {
			// The reference client decrypts every batch echo with the original
			// request sequence, rather than incrementing it per response.
			if err := writeBHTTPMasked(conn, wire.StatusOK, body, req); err != nil {
				return err
			}
		}
		return nil

	case bhttpModeUpload:
		if req.seq == 0 && len(req.payload) == 0 {
			if !sessions.register(req.session) {
				return writeBHTTPError(conn, "session limit reached")
			}
			return wire.WriteResponse(conn, wire.StatusOK, nil)
		}
		session := sessions.get(req.session)
		if session == nil {
			return writeBHTTPError(conn, "unknown session")
		}
		if len(req.payload) > maxChunk {
			return writeBHTTPError(conn, "upload too large")
		}
		if req.seq == 1 && len(req.payload) >= len(bhttpOpenMagic) && bytes.Equal(req.payload[:len(bhttpOpenMagic)], bhttpOpenMagic[:]) {
			supplied, host, port, err := parseOpen(req.payload[len(bhttpOpenMagic):])
			if err != nil {
				return writeBHTTPError(conn, err.Error())
			}
			if !tokenEqual(supplied, ctx.token) {
				return writeBHTTPError(conn, "authentication failed")
			}
			session.mu.Lock()
			alreadyOpen := session.stream != nil
			session.mu.Unlock()
			if alreadyOpen {
				return wire.WriteResponse(conn, wire.StatusOK, nil)
			}
			dialCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			target, err := dialTarget(dialCtx, host, port, ctx.allowPrivate, ctx.cache, ctx.tcpBuffer)
			cancel()
			if err != nil {
				return writeBHTTPError(conn, err.Error())
			}
			_, internalCarrier := lookupInternalTarget(host, port)
			stream := newStreamSession(req.session, target, fmt.Sprintf("%s:%d", host, port), maxChunk, ctx.maxBuffer, internalCarrier, ctx.debug)
			session.mu.Lock()
			if session.stream == nil {
				session.stream = stream
				session.lastSeen = time.Now()
				stream = nil
			}
			session.mu.Unlock()
			if stream != nil {
				stream.close()
			}
			if ctx.debug != nil && ctx.debug.enabled {
				ctx.debug.logf("BP OPEN sid=%x target=%s:%d", req.session[:4], host, port)
			}
			return wire.WriteResponse(conn, wire.StatusOK, nil)
		}

		session.mu.Lock()
		stream := session.stream
		session.mu.Unlock()
		if stream != nil {
			if req.seq < 2 {
				return writeBHTTPError(conn, "bad upload sequence")
			}
			if err := stream.upload(req.seq-2, req.payload); err != nil {
				return writeBHTTPError(conn, err.Error())
			}
		}
		session.mu.Lock()
		session.uploaded += uint64(len(req.payload))
		session.lastSeen = time.Now()
		session.mu.Unlock()
		return wire.WriteResponse(conn, wire.StatusOK, nil)

	case bhttpModeDownload:
		session := sessions.get(req.session)
		if session == nil {
			return writeBHTTPError(conn, "unknown session")
		}
		session.mu.Lock()
		stream := session.stream
		session.mu.Unlock()
		if stream == nil {
			// The reference transport has no observable downstream producer.
			return wire.WriteResponse(conn, wire.StatusOK, nil)
		}
		limit := int(req.value)
		if limit < 1 {
			limit = 1
		}
		if limit > maxChunk {
			limit = maxChunk
		}
		data, status, err := stream.readAt(req.seq, limit, ctx.pollWait)
		if err != nil {
			return writeBHTTPError(conn, err.Error())
		}
		switch status {
		case wire.StatusData:
			return writeBHTTPData(conn, req, data)
		case wire.StatusEOF:
			return wire.WriteResponse(conn, wire.StatusEOF, nil)
		default:
			return wire.WriteResponse(conn, wire.StatusOK, nil)
		}

	case bhttpModeBatchDownload:
		session := sessions.get(req.session)
		if session == nil {
			return writeBHTTPError(conn, "unknown session")
		}
		if len(req.payload) != 6 {
			return writeBHTTPError(conn, "bad batch download request")
		}
		count := int(binary.BigEndian.Uint16(req.payload[4:6]))
		limit := int(binary.BigEndian.Uint32(req.payload[:4]))
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
		session.mu.Lock()
		stream := session.stream
		session.mu.Unlock()
		offset := req.seq
		for i := 0; i < count; i++ {
			if stream == nil {
				if err := wire.WriteResponse(conn, wire.StatusOK, nil); err != nil {
					return err
				}
				continue
			}
			wait := time.Duration(0)
			if i == 0 {
				wait = ctx.pollWait
			}
			data, status, err := stream.readAt(offset, limit, wait)
			if err != nil {
				return writeBHTTPError(conn, err.Error())
			}
			switch status {
			case wire.StatusData:
				if err := writeBHTTPData(conn, req, data); err != nil {
					return err
				}
				offset += uint64(len(data))
			case wire.StatusEOF:
				if err := wire.WriteResponse(conn, wire.StatusEOF, nil); err != nil {
					return err
				}
			default:
				if err := wire.WriteResponse(conn, wire.StatusOK, nil); err != nil {
					return err
				}
			}
		}
		return nil

	case bhttpModeACK:
		session := sessions.get(req.session)
		if session == nil {
			return writeBHTTPError(conn, "unknown session")
		}
		// Dragon's BP extension sends an explicit close marker. Reference BP
		// clients continue to use an empty ACK, while Dragon clients release the
		// target socket and buffered download data immediately instead of waiting
		// for the idle-session reaper.
		if bytes.Equal(req.payload, bpCloseMagic[:]) {
			sessions.remove(req.session)
			return wire.WriteResponse(conn, wire.StatusOK, nil)
		}
		session.mu.Lock()
		if req.seq > session.acked {
			session.acked = req.seq
		}
		session.lastSeen = time.Now()
		stream := session.stream
		session.mu.Unlock()
		if stream != nil {
			stream.ack(req.seq)
		}
		return wire.WriteResponse(conn, wire.StatusOK, nil)
	}
	return writeBHTTPError(conn, "unknown mode")
}

type binaryFlavor byte

const (
	binaryFlavorUnknown binaryFlavor = iota
	binaryFlavorDragon
	binaryFlavorBHTTP
)

func isBHTTPProbe(payload []byte) bool {
	return len(payload) >= 4 && bytes.Equal(payload[:4], bhttpProbeMagic[:])
}

// handleBinary auto-detects the two protocols without changing the native B
// header space. BHTTP is clear-header only; Dragon profiles and cover-prefaced
// connections continue through the existing handler unchanged.
func handleBinary(
	conn net.Conn,
	headerMask byte,
	clearPayload bool,
	token string,
	allowPrivate bool,
	cache *dnsCache,
	tcpBuffer int,
	manager *streamManager,
	bhttp *bhttpSessionManager,
	chunkMax int,
	bufferBytes int,
	pollWait time.Duration,
	debug *serverDebug,
) {
	reader := bufio.NewReader(conn)
	bhttpContext := &bhttpServerContext{
		sessions:     bhttp,
		token:        token,
		allowPrivate: allowPrivate,
		cache:        cache,
		tcpBuffer:    tcpBuffer,
		maxChunk:     chunkMax,
		maxBuffer:    bufferBytes,
		pollWait:     pollWait,
		debug:        debug,
	}
	flavor := binaryFlavorUnknown
	deadline := newIdleDeadline(conn, 30*time.Second)
	for {
		if deadline.refresh() != nil {
			return
		}
		header, err := peekBinaryHeader(reader, headerMask)
		if err != nil {
			return
		}

		if flavor == binaryFlavorUnknown {
			switch header.mode {
			case bhttpModeProbe:
				// Probe framing is shared, so consume it once and use its magic
				// to select BHP1 or DTP2 without losing any bytes.
				req, err := wire.ReadRequestProfileEncoding(reader, headerMask, clearPayload)
				if err != nil {
					return
				}
				if isBHTTPProbe(req.Payload) {
					flavor = binaryFlavorBHTTP
					breq := bhttpRequest{mode: req.Mode, session: req.Session, seq: req.Seq, value: uint32(len(req.Payload)), payload: req.Payload, headerMask: headerMask, clear: clearPayload}
					if processBHTTPRequest(conn, breq, bhttpContext) != nil {
						return
					}
					continue
				}
				flavor = binaryFlavorDragon
				if processWireRequest(conn, req, token, allowPrivate, cache, tcpBuffer, manager, chunkMax, bufferBytes, pollWait, debug) != nil {
					return
				}
				continue

			case bhttpModeUpload:
				if bhttp.get(header.session) != nil || (header.seq == 0 && header.length == 0) {
					flavor = binaryFlavorBHTTP
				} else {
					flavor = binaryFlavorDragon
				}
			case bhttpModeDownload:
				if bhttp.get(header.session) != nil {
					flavor = binaryFlavorBHTTP
				} else if manager.get(header.session) != nil {
					flavor = binaryFlavorDragon
				} else {
					// The BHTTP unknown-session test sends only a header whose
					// length field is a hint. Consume no nonexistent body.
					if _, err := readBHTTPRequest(reader, headerMask, clearPayload); err == nil {
						_ = writeBHTTPError(conn, "unknown session")
					}
					return
				}
			case bhttpModeBatchDownload:
				if bhttp.get(header.session) != nil || header.length == 6 {
					flavor = binaryFlavorBHTTP
				} else {
					flavor = binaryFlavorDragon
				}
			case bhttpModeACK:
				if bhttp.get(header.session) != nil {
					flavor = binaryFlavorBHTTP
				} else {
					flavor = binaryFlavorDragon
				}
			default:
				return
			}
		}

		if flavor == binaryFlavorBHTTP {
			req, err := readBHTTPRequest(reader, headerMask, clearPayload)
			if err != nil || processBHTTPRequest(conn, req, bhttpContext) != nil {
				return
			}
			continue
		}

		req, err := wire.ReadRequestProfileEncoding(reader, headerMask, clearPayload)
		if err != nil {
			return
		}
		if processWireRequest(conn, req, token, allowPrivate, cache, tcpBuffer, manager, chunkMax, bufferBytes, pollWait, debug) != nil {
			return
		}
	}
}
