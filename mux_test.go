package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dragontcp/internal/cover"
	"dragontcp/internal/wire"
)

// echoServer starts a TCP server that simply echoes back whatever it reads.
func startEchoTarget(t *testing.T) (net.Listener, string, int) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start echo listener: %v", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}(c)
		}
	}()
	return ln, "127.0.0.1", addr.Port
}

func encodeOpenPayload(token, host string, port int) []byte {
	tb := []byte(token)
	hb := []byte(host)
	p := make([]byte, 6+len(tb)+len(hb))
	binary.BigEndian.PutUint16(p[0:2], uint16(len(tb)))
	binary.BigEndian.PutUint16(p[2:4], uint16(len(hb)))
	binary.BigEndian.PutUint16(p[4:6], uint16(port))
	copy(p[6:], tb)
	copy(p[6+len(tb):], hb)
	return p
}

func encodeDownloadPayload(ack uint64, limit uint32, count uint16) []byte {
	p := make([]byte, 14)
	binary.BigEndian.PutUint64(p[0:8], ack)
	binary.BigEndian.PutUint32(p[8:12], limit)
	binary.BigEndian.PutUint16(p[12:14], count)
	return p
}

func TestMuxEndToEndSingleSession(t *testing.T) {
	echoLn, echoHost, echoPort := startEchoTarget(t)
	defer echoLn.Close()

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	manager := newStreamManager(time.Minute, nil)
	cache := newDNSCache(time.Minute, 1024)
	mc := newMuxServerConn(serverConn, 0, false)

	go handleMuxConnection(mc, "", true, cache, 0, manager, 65536, 1024*1024, 50*time.Millisecond, nil)

	var sid wire.SessionID
	copy(sid[:], []byte("mux-session-0001"))

	// 1. ModeProbe
	probePayload := append([]byte(nil), wire.ProbeMagic[:]...)
	probePayload = append(probePayload, wire.ProbeKeepalive, 0, 0, 0, 0, 0, 0) // keepalive probe
	if err := wire.WriteMuxRequest(clientConn, wire.ModeProbe, sid, 0, 10, probePayload); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	resp, err := wire.ReadMuxResponse(clientConn)
	if err != nil || resp.Status != wire.StatusOK || resp.RequestID != 10 {
		t.Fatalf("probe response mismatch: resp=%+v err=%v", resp, err)
	}

	// 2. ModeOpen
	openPayload := encodeOpenPayload("", echoHost, echoPort)
	if err := wire.WriteMuxRequest(clientConn, wire.ModeOpen, sid, 0, 20, openPayload); err != nil {
		t.Fatalf("write open: %v", err)
	}
	resp, err = wire.ReadMuxResponse(clientConn)
	if err != nil || resp.Status != wire.StatusOK || resp.RequestID != 20 {
		t.Fatalf("open response mismatch: resp=%+v err=%v", resp, err)
	}

	// 3. ModeUpload
	uploadData := []byte("hello multiplexed dragon")
	if err := wire.WriteMuxRequest(clientConn, wire.ModeUpload, sid, 0, 30, uploadData); err != nil {
		t.Fatalf("write upload: %v", err)
	}
	resp, err = wire.ReadMuxResponse(clientConn)
	if err != nil || resp.Status != wire.StatusOK || resp.RequestID != 30 {
		t.Fatalf("upload response mismatch: resp=%+v err=%v", resp, err)
	}

	// 4. ModeDownload
	dlPayload := encodeDownloadPayload(0, 1024, 1)
	if err := wire.WriteMuxRequest(clientConn, wire.ModeDownload, sid, 0, 40, dlPayload); err != nil {
		t.Fatalf("write download: %v", err)
	}
	// Expect StatusData followed by StatusWait
	resp, err = wire.ReadMuxResponse(clientConn)
	if err != nil || resp.Status != wire.StatusData || resp.RequestID != 40 {
		t.Fatalf("expected StatusData, got: %+v, err: %v", resp, err)
	}
	decoded := wire.DecodeMaskedResponse(resp.Status, resp.Body, sid, wire.ModeDownload, 0)
	if !bytes.Equal(decoded, uploadData) {
		t.Fatalf("echoed data mismatch: got %q, want %q", decoded, uploadData)
	}

	// Wait frame
	resp, err = wire.ReadMuxResponse(clientConn)
	if err != nil || resp.Status != wire.StatusWait || resp.RequestID != 40 {
		t.Fatalf("expected StatusWait, got: %+v, err: %v", resp, err)
	}

	// 5. ModeClose
	if err := wire.WriteMuxRequest(clientConn, wire.ModeClose, sid, 0, 50, nil); err != nil {
		t.Fatalf("write close: %v", err)
	}
	resp, err = wire.ReadMuxResponse(clientConn)
	if err != nil || resp.Status != wire.StatusOK || resp.RequestID != 50 {
		t.Fatalf("close response mismatch: resp=%+v err=%v", resp, err)
	}
}

func TestMuxConcurrentMultiSession(t *testing.T) {
	echoLn, echoHost, echoPort := startEchoTarget(t)
	defer echoLn.Close()

	// Use real local TCP socket for full duplex concurrent networking
	serverLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer serverLn.Close()

	manager := newStreamManager(time.Minute, nil)
	cache := newDNSCache(time.Minute, 1024)

	var serverErr error
	go func() {
		conn, err := serverLn.Accept()
		if err != nil {
			serverErr = err
			return
		}
		defer conn.Close()
		mc := newMuxServerConn(conn, 0, false)
		handleMuxConnection(mc, "", true, cache, 0, manager, 65536, 1024*1024, 20*time.Millisecond, nil)
	}()

	clientConn, err := net.Dial("tcp", serverLn.Addr().String())
	if err != nil {
		t.Fatalf("dial server failed: %v", err)
	}
	defer clientConn.Close()

	numSessions := 8
	var clientWriteMu sync.Mutex

	// Response dispatcher on client side
	type pendingResp struct {
		ch chan wire.MuxResponse
	}
	pending := make(map[uint32]*pendingResp)
	var pendingMu sync.Mutex

	registerReq := func(reqID uint32) chan wire.MuxResponse {
		ch := make(chan wire.MuxResponse, 8)
		pendingMu.Lock()
		pending[reqID] = &pendingResp{ch: ch}
		pendingMu.Unlock()
		return ch
	}

	go func() {
		for {
			resp, err := wire.ReadMuxResponse(clientConn)
			if err != nil {
				return
			}
			pendingMu.Lock()
			pr := pending[resp.RequestID]
			pendingMu.Unlock()
			if pr != nil {
				pr.ch <- resp
			}
		}
	}()

	var wg sync.WaitGroup
	for s := 0; s < numSessions; s++ {
		wg.Add(1)
		sessionIndex := s
		go func() {
			defer wg.Done()
			var sid wire.SessionID
			copy(sid[:], fmt.Sprintf("session-%08d", sessionIndex))

			baseReqID := uint32(sessionIndex * 1000)

			// 1. Open
			openReqID := baseReqID + 1
			openCh := registerReq(openReqID)
			clientWriteMu.Lock()
			_ = wire.WriteMuxRequest(clientConn, wire.ModeOpen, sid, 0, openReqID, encodeOpenPayload("", echoHost, echoPort))
			clientWriteMu.Unlock()
			openResp := <-openCh
			if openResp.Status != wire.StatusOK {
				t.Errorf("session %d open failed: %d", sessionIndex, openResp.Status)
				return
			}

			// 2. Upload
			msg := []byte(fmt.Sprintf("concurrent payload from session %d", sessionIndex))
			uploadReqID := baseReqID + 2
			uploadCh := registerReq(uploadReqID)
			clientWriteMu.Lock()
			_ = wire.WriteMuxRequest(clientConn, wire.ModeUpload, sid, 0, uploadReqID, msg)
			clientWriteMu.Unlock()
			upResp := <-uploadCh
			if upResp.Status != wire.StatusOK {
				t.Errorf("session %d upload failed: %d", sessionIndex, upResp.Status)
				return
			}

			// 3. Download
			dlReqID := baseReqID + 3
			dlCh := registerReq(dlReqID)
			clientWriteMu.Lock()
			_ = wire.WriteMuxRequest(clientConn, wire.ModeDownload, sid, 0, dlReqID, encodeDownloadPayload(0, 1024, 1))
			clientWriteMu.Unlock()

			dlDataResp := <-dlCh
			if dlDataResp.Status != wire.StatusData {
				t.Errorf("session %d expected StatusData, got %d", sessionIndex, dlDataResp.Status)
				return
			}
			decoded := wire.DecodeMaskedResponse(dlDataResp.Status, dlDataResp.Body, sid, wire.ModeDownload, 0)
			if !bytes.Equal(decoded, msg) {
				t.Errorf("session %d payload mismatch: got %q, want %q", sessionIndex, decoded, msg)
				return
			}

			dlWaitResp := <-dlCh
			if dlWaitResp.Status != wire.StatusWait {
				t.Errorf("session %d expected StatusWait, got %d", sessionIndex, dlWaitResp.Status)
				return
			}

			// 4. Close
			closeReqID := baseReqID + 4
			closeCh := registerReq(closeReqID)
			clientWriteMu.Lock()
			_ = wire.WriteMuxRequest(clientConn, wire.ModeClose, sid, 0, closeReqID, nil)
			clientWriteMu.Unlock()
			closeResp := <-closeCh
			if closeResp.Status != wire.StatusOK {
				t.Errorf("session %d close failed: %d", sessionIndex, closeResp.Status)
			}
		}()
	}

	wg.Wait()
	if serverErr != nil {
		t.Fatalf("server encountered error: %v", serverErr)
	}
}

func TestMuxHybridCoexistence(t *testing.T) {
	echoLn, echoHost, echoPort := startEchoTarget(t)
	defer echoLn.Close()

	// Start server that runs through handle() pipeline with sniffWire
	serverLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer serverLn.Close()

	manager := newStreamManager(time.Minute, nil)
	bhttpManager := newBHTTPSessionManager(time.Minute, 1000)
	xorManager := newChunkManager(time.Minute, nil)
	cache := newDNSCache(time.Minute, 1024)
	slots := make(chan struct{}, 100)

	go func() {
		for {
			conn, err := serverLn.Accept()
			if err != nil {
				return
			}
			slots <- struct{}{}
			go handle(conn, "", true, cache, 0, slots, manager, bhttpManager, xorManager,
				65536, 1024*1024, 1024*1024, 20*time.Millisecond, nil)
		}
	}()

	// 1. Client v1 (29-byte header)
	v1Conn, err := net.Dial("tcp", serverLn.Addr().String())
	if err != nil {
		t.Fatalf("dial v1: %v", err)
	}
	defer v1Conn.Close()

	var sid1 wire.SessionID
	copy(sid1[:], []byte("legacy-v1-client"))

	// v1 ModeOpen
	openPayload1 := encodeOpenPayload("", echoHost, echoPort)
	if err := wire.WriteRequest(v1Conn, wire.ModeOpen, sid1, 0, openPayload1); err != nil {
		t.Fatalf("v1 open: %v", err)
	}
	status1, _, err := wire.ReadResponse(v1Conn)
	if err != nil || status1 != wire.StatusOK {
		t.Fatalf("v1 open failed: status=%d err=%v", status1, err)
	}

	// 2. Client v2 (33-byte header, multiplexed)
	v2Conn, err := net.Dial("tcp", serverLn.Addr().String())
	if err != nil {
		t.Fatalf("dial v2: %v", err)
	}
	defer v2Conn.Close()

	var sid2 wire.SessionID
	copy(sid2[:], []byte("mux-v2-client-99"))

	// v2 ModeOpen
	openPayload2 := encodeOpenPayload("", echoHost, echoPort)
	if err := wire.WriteMuxRequest(v2Conn, wire.ModeOpen, sid2, 0, 777, openPayload2); err != nil {
		t.Fatalf("v2 open: %v", err)
	}
	resp2, err := wire.ReadMuxResponse(v2Conn)
	if err != nil || resp2.Status != wire.StatusOK || resp2.RequestID != 777 {
		t.Fatalf("v2 open failed: resp=%+v err=%v", resp2, err)
	}

	// Both clients send upload
	msg1 := []byte("v1 message")
	if err := wire.WriteRequest(v1Conn, wire.ModeUpload, sid1, 0, msg1); err != nil {
		t.Fatalf("v1 upload: %v", err)
	}
	status1, _, err = wire.ReadResponse(v1Conn)
	if err != nil || status1 != wire.StatusOK {
		t.Fatalf("v1 upload status=%d err=%v", status1, err)
	}

	msg2 := []byte("v2 message")
	if err := wire.WriteMuxRequest(v2Conn, wire.ModeUpload, sid2, 0, 778, msg2); err != nil {
		t.Fatalf("v2 upload: %v", err)
	}
	resp2, err = wire.ReadMuxResponse(v2Conn)
	if err != nil || resp2.Status != wire.StatusOK || resp2.RequestID != 778 {
		t.Fatalf("v2 upload resp=%+v err=%v", resp2, err)
	}

	// Both clients download echoed data
	if err := wire.WriteRequest(v1Conn, wire.ModeDownload, sid1, 0, encodeDownloadPayload(0, 1024, 1)); err != nil {
		t.Fatalf("v1 download req: %v", err)
	}
	status1, body1, err := wire.ReadResponse(v1Conn)
	if err != nil || status1 != wire.StatusData {
		t.Fatalf("v1 download status=%d err=%v", status1, err)
	}
	dec1 := wire.DecodeMaskedResponse(status1, body1, sid1, wire.ModeDownload, 0)
	if !bytes.Equal(dec1, msg1) {
		t.Fatalf("v1 echo mismatch: got %q, want %q", dec1, msg1)
	}

	if err := wire.WriteMuxRequest(v2Conn, wire.ModeDownload, sid2, 0, 779, encodeDownloadPayload(0, 1024, 1)); err != nil {
		t.Fatalf("v2 download req: %v", err)
	}
	resp2, err = wire.ReadMuxResponse(v2Conn)
	if err != nil || resp2.Status != wire.StatusData || resp2.RequestID != 779 {
		t.Fatalf("v2 download status data: resp=%+v err=%v", resp2, err)
	}
	dec2 := wire.DecodeMaskedResponse(resp2.Status, resp2.Body, sid2, wire.ModeDownload, 0)
	if !bytes.Equal(dec2, msg2) {
		t.Fatalf("v2 echo mismatch: got %q, want %q", dec2, msg2)
	}
}

func TestMuxCoverProfileEndToEnd(t *testing.T) {
	echoLn, echoHost, echoPort := startEchoTarget(t)
	defer echoLn.Close()

	serverLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer serverLn.Close()

	manager := newStreamManager(time.Minute, nil)
	bhttpManager := newBHTTPSessionManager(time.Minute, 1000)
	xorManager := newChunkManager(time.Minute, nil)
	cache := newDNSCache(time.Minute, 1024)
	slots := make(chan struct{}, 100)

	go func() {
		for {
			conn, err := serverLn.Accept()
			if err != nil {
				return
			}
			slots <- struct{}{}
			go handle(conn, "", true, cache, 0, slots, manager, bhttpManager, xorManager,
				65536, 1024*1024, 1024*1024, 20*time.Millisecond, nil)
		}
	}()

	conn, err := net.Dial("tcp", serverLn.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	mask := byte(0x60)
	profile := cover.Profile{
		Enabled:    true,
		ID:         0x55aa,
		Padding:    16,
		HeaderMask: mask,
		Clear:      true,
		MuxV2:      true,
	}
	if err := cover.WritePreface(conn, profile); err != nil {
		t.Fatalf("write preface: %v", err)
	}

	var sid wire.SessionID
	copy(sid[:], []byte("cover-mux-sess"))

	// 1. Open
	openPayload := encodeOpenPayload("", echoHost, echoPort)
	if err := wire.WriteMuxRequestProfileEncoding(conn, wire.ModeOpen, sid, 0, 101, openPayload, mask, true); err != nil {
		t.Fatalf("write open: %v", err)
	}
	resp, err := wire.ReadMuxResponseProfile(conn, mask)
	if err != nil || resp.Status != wire.StatusOK || resp.RequestID != 101 {
		t.Fatalf("open response: resp=%+v err=%v", resp, err)
	}

	// 2. Upload
	uploadData := []byte("covered clear mux message")
	if err := wire.WriteMuxRequestProfileEncoding(conn, wire.ModeUpload, sid, 0, 102, uploadData, mask, true); err != nil {
		t.Fatalf("write upload: %v", err)
	}
	resp, err = wire.ReadMuxResponseProfile(conn, mask)
	if err != nil || resp.Status != wire.StatusOK || resp.RequestID != 102 {
		t.Fatalf("upload response: resp=%+v err=%v", resp, err)
	}

	// 3. Download
	dlPayload := encodeDownloadPayload(0, 1024, 1)
	if err := wire.WriteMuxRequestProfileEncoding(conn, wire.ModeDownload, sid, 0, 103, dlPayload, mask, true); err != nil {
		t.Fatalf("write download: %v", err)
	}
	resp, err = wire.ReadMuxResponseProfile(conn, mask)
	if err != nil || resp.Status != wire.StatusData || resp.RequestID != 103 {
		t.Fatalf("expected StatusData: resp=%+v err=%v", resp, err)
	}
	if !bytes.Equal(resp.Body, uploadData) {
		t.Fatalf("clear body mismatch: got %q, want %q", resp.Body, uploadData)
	}

	resp, err = wire.ReadMuxResponseProfile(conn, mask)
	if err != nil || resp.Status != wire.StatusWait || resp.RequestID != 103 {
		t.Fatalf("expected StatusWait: resp=%+v err=%v", resp, err)
	}

	// 4. Close
	if err := wire.WriteMuxRequestProfileEncoding(conn, wire.ModeClose, sid, 0, 104, nil, mask, true); err != nil {
		t.Fatalf("write close: %v", err)
	}
	resp, err = wire.ReadMuxResponseProfile(conn, mask)
	if err != nil || resp.Status != wire.StatusOK || resp.RequestID != 104 {
		t.Fatalf("close response: resp=%+v err=%v", resp, err)
	}
}

func TestMuxErrorsAndValidation(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	manager := newStreamManager(time.Minute, nil)
	cache := newDNSCache(time.Minute, 1024)
	mc := newMuxServerConn(serverConn, 0, false)

	go handleMuxConnection(mc, "secret-token", false, cache, 0, manager, 65536, 1024*1024, 50*time.Millisecond, nil)

	var sid wire.SessionID
	copy(sid[:], []byte("err-sess-001"))

	// 1. Upload to non-existent session
	if err := wire.WriteMuxRequest(clientConn, wire.ModeUpload, sid, 0, 501, []byte("data")); err != nil {
		t.Fatalf("write: %v", err)
	}
	resp, err := wire.ReadMuxResponse(clientConn)
	if err != nil || resp.Status != wire.StatusError || resp.RequestID != 501 {
		t.Fatalf("expected error: %+v, err: %v", resp, err)
	}
	if !bytes.Contains(resp.Body, []byte("unknown session")) {
		t.Fatalf("expected unknown session error, got %q", resp.Body)
	}

	// 2. Open with wrong token
	badAuthPayload := encodeOpenPayload("wrong-token", "127.0.0.1", 9999)
	if err := wire.WriteMuxRequest(clientConn, wire.ModeOpen, sid, 0, 502, badAuthPayload); err != nil {
		t.Fatalf("write: %v", err)
	}
	resp, err = wire.ReadMuxResponse(clientConn)
	if err != nil || resp.Status != wire.StatusError || resp.RequestID != 502 {
		t.Fatalf("expected auth error: %+v, err: %v", resp, err)
	}
	if !bytes.Contains(resp.Body, []byte("authentication failed")) {
		t.Fatalf("expected authentication failed, got %q", resp.Body)
	}

	// 3. Open with unreachable target (allowPrivate is false, 127.0.0.1 blocked)
	authPayload := encodeOpenPayload("secret-token", "127.0.0.1", 9999)
	if err := wire.WriteMuxRequest(clientConn, wire.ModeOpen, sid, 0, 503, authPayload); err != nil {
		t.Fatalf("write: %v", err)
	}
	resp, err = wire.ReadMuxResponse(clientConn)
	if err != nil || resp.Status != wire.StatusError || resp.RequestID != 503 {
		t.Fatalf("expected private target blocked error: %+v, err: %v", resp, err)
	}
}

func TestMuxDownloadBatchDrainsBufferImmediately(t *testing.T) {
	echoLn, echoHost, echoPort := startEchoTarget(t)
	defer echoLn.Close()

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	manager := newStreamManager(time.Minute, nil)
	cache := newDNSCache(time.Minute, 1024)
	mc := newMuxServerConn(serverConn, 0, false)

	go handleMuxConnection(mc, "secret-token", true, cache, 0, manager, 65536, 1024*1024, 200*time.Millisecond, nil)

	var sid wire.SessionID
	sid[0] = 0xbb
	sid[15] = 0xcc

	// Open session
	openPayload := encodeOpenPayload("secret-token", echoHost, echoPort)
	if err := wire.WriteMuxRequest(clientConn, wire.ModeOpen, sid, 0, 601, openPayload); err != nil {
		t.Fatalf("write open: %v", err)
	}
	resp, err := wire.ReadMuxResponse(clientConn)
	if err != nil || resp.Status != wire.StatusOK || resp.RequestID != 601 {
		t.Fatalf("open mismatch: resp=%+v err=%v", resp, err)
	}

	// Upload 100 bytes to echo server
	uploadData := bytes.Repeat([]byte("M"), 100)
	if err := wire.WriteMuxRequest(clientConn, wire.ModeUpload, sid, 0, 602, uploadData); err != nil {
		t.Fatalf("write upload: %v", err)
	}
	resp, err = wire.ReadMuxResponse(clientConn)
	if err != nil || resp.Status != wire.StatusOK || resp.RequestID != 602 {
		t.Fatalf("upload mismatch: resp=%+v err=%v", resp, err)
	}

	// Wait briefly for echo server to respond and streamSession to buffer 100 bytes
	time.Sleep(30 * time.Millisecond)

	// Request batch download with limit=50 and count=5.
	// Buffer has 100 bytes, so records 0 and 1 will have data (50 bytes each),
	// and record 2 will see an empty buffer and return StatusWait immediately!
	// Records 3 and 4 must not block or be sent.
	start := time.Now()
	dlPayload := encodeDownloadPayload(0, 50, 5)
	if err := wire.WriteMuxRequest(clientConn, wire.ModeDownload, sid, 0, 603, dlPayload); err != nil {
		t.Fatalf("write download: %v", err)
	}

	// 1st record: 50 bytes data
	resp, err = wire.ReadMuxResponse(clientConn)
	if err != nil || resp.Status != wire.StatusData || resp.RequestID != 603 {
		t.Fatalf("record 0 expected StatusData: resp=%+v err=%v", resp, err)
	}
	if len(resp.Body) != 50 {
		t.Fatalf("record 0 expected 50 bytes, got %d", len(resp.Body))
	}

	// 2nd record: 50 bytes data
	resp, err = wire.ReadMuxResponse(clientConn)
	if err != nil || resp.Status != wire.StatusData || resp.RequestID != 603 {
		t.Fatalf("record 1 expected StatusData: resp=%+v err=%v", resp, err)
	}
	if len(resp.Body) != 50 {
		t.Fatalf("record 1 expected 50 bytes, got %d", len(resp.Body))
	}

	// 3rd record: buffer empty -> returns StatusWait immediately
	resp, err = wire.ReadMuxResponse(clientConn)
	elapsed := time.Since(start)
	if err != nil || resp.Status != wire.StatusWait || resp.RequestID != 603 {
		t.Fatalf("record 2 expected StatusWait: resp=%+v err=%v", resp, err)
	}

	// The whole download batch should finish promptly without waiting 200ms long-poll for remaining records
	if elapsed >= 150*time.Millisecond {
		t.Fatalf("batch download took too long (%v), expected prompt completion (<150ms)", elapsed)
	}

	// Close session
	if err := wire.WriteMuxRequest(clientConn, wire.ModeClose, sid, 0, 604, nil); err != nil {
		t.Fatalf("write close: %v", err)
	}
	resp, err = wire.ReadMuxResponse(clientConn)
	if err != nil || resp.Status != wire.StatusOK || resp.RequestID != 604 {
		t.Fatalf("close mismatch: resp=%+v err=%v", resp, err)
	}
}

func TestHTTPPayloadHandshakeAndMuxV2(t *testing.T) {
	echoLn, echoHost, echoPort := startEchoTarget(t)
	defer echoLn.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	manager := newStreamManager(time.Minute, nil)
	bhttpManager := newBHTTPSessionManager(time.Minute, 1000)
	xorManager := newChunkManager(time.Minute, nil)
	cache := newDNSCache(time.Minute, 1024)
	slots := make(chan struct{}, 100)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			slots <- struct{}{}
			go handle(conn, "", true, cache, 0, slots, manager, bhttpManager, xorManager, 1048576, 4194304, 2097152, 200*time.Millisecond, nil)
		}
	}()

	clientConn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()

	// 1. Send HTTP payload with simulated 4G zero-rated Host and WebSocket upgrade
	httpReq := "GET / HTTP/1.1\r\nHost: portalrecarga.vivo.com.br\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"
	if _, err := clientConn.Write([]byte(httpReq)); err != nil {
		t.Fatalf("write http payload: %v", err)
	}

	// 2. Read HTTP 101 response
	br := bufio.NewReader(clientConn)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read status line: %v", err)
	}
	if !strings.Contains(statusLine, "101 Switching Protocols") {
		t.Fatalf("expected 101 Switching Protocols, got: %s", statusLine)
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read header: %v", err)
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}

	// 3. Now speak DragonTCP Mux v2 over the upgraded stream!
	var sid wire.SessionID
	copy(sid[:], []byte("http-payload-sess"))

	// ModeOpen
	openPayload := encodeOpenPayload("", echoHost, echoPort)
	if err := wire.WriteMuxRequest(clientConn, wire.ModeOpen, sid, 0, 701, openPayload); err != nil {
		t.Fatalf("write open: %v", err)
	}
	resp, err := wire.ReadMuxResponse(br)
	if err != nil || resp.Status != wire.StatusOK || resp.RequestID != 701 {
		t.Fatalf("open mismatch: resp=%+v err=%v", resp, err)
	}

	// ModeUpload
	uploadData := []byte("hello through http payload upgraded tunnel")
	if err := wire.WriteMuxRequest(clientConn, wire.ModeUpload, sid, 0, 702, uploadData); err != nil {
		t.Fatalf("write upload: %v", err)
	}
	resp, err = wire.ReadMuxResponse(br)
	if err != nil || resp.Status != wire.StatusOK || resp.RequestID != 702 {
		t.Fatalf("upload mismatch: resp=%+v err=%v", resp, err)
	}

	// ModeDownload
	dlPayload := encodeDownloadPayload(0, 1024, 1)
	if err := wire.WriteMuxRequest(clientConn, wire.ModeDownload, sid, 0, 703, dlPayload); err != nil {
		t.Fatalf("write download: %v", err)
	}
	resp, err = wire.ReadMuxResponse(br)
	if err != nil || resp.Status != wire.StatusData || resp.RequestID != 703 {
		t.Fatalf("download mismatch: resp=%+v err=%v", resp, err)
	}
	decoded := wire.DecodeMaskedResponse(resp.Status, resp.Body, sid, wire.ModeDownload, 0)
	if !bytes.Equal(decoded, uploadData) {
		t.Fatalf("echo body mismatch: got %q, want %q", decoded, uploadData)
	}

	// Read StatusWait ending the download request
	resp, err = wire.ReadMuxResponse(br)
	if err != nil || resp.Status != wire.StatusWait || resp.RequestID != 703 {
		t.Fatalf("wait mismatch: resp=%+v err=%v", resp, err)
	}

	// ModeClose
	if err := wire.WriteMuxRequest(clientConn, wire.ModeClose, sid, 0, 704, nil); err != nil {
		t.Fatalf("write close: %v", err)
	}
	resp, err = wire.ReadMuxResponse(br)
	if err != nil || resp.Status != wire.StatusOK || resp.RequestID != 704 {
		t.Fatalf("close mismatch: resp=%+v err=%v", resp, err)
	}
}

func TestMuxParallelWorkersIperf(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	cache := newDNSCache(time.Minute, 1024)
	manager := newStreamManager(time.Minute, nil)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				mc := newMuxServerConn(c, 0, false)
				handleMuxConnection(mc, "", true, cache, 0, manager, 1024*1024, 2*1024*1024, 10*time.Millisecond, nil)
			}(conn)
		}
	}()

	const (
		chunkSize = 16 * 1024
		duration  = 100 * time.Millisecond
	)
	workers := calculateParallelWorkers(chunkSize)
	if workers != 64 {
		t.Fatalf("expected 64 workers, got %d", workers)
	}

	started := time.Now()
	deadline := started.Add(duration)

	var totalBytes atomic.Int64
	var totalErrors atomic.Int64
	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			c, err := net.Dial("tcp", ln.Addr().String())
			if err != nil {
				totalErrors.Add(1)
				return
			}
			defer c.Close()

			var sid wire.SessionID
			binary.BigEndian.PutUint64(sid[0:8], uint64(workerID+1))
			reqID := uint32(workerID * 1000)

			for time.Now().Before(deadline) {
				_ = c.SetDeadline(time.Now().Add(time.Second))
				// 1. Upload probe
				reqID++
				payloadUp := makeTestProbePayload(wire.ProbeIperfUpload, chunkSize, chunkSize, "")
				if err := wire.WriteMuxRequest(c, wire.ModeProbe, sid, 0, reqID, payloadUp); err != nil {
					totalErrors.Add(1)
					return
				}
				resp, err := wire.ReadMuxResponse(c)
				if err != nil || resp.Status != wire.StatusOK || resp.RequestID != reqID {
					totalErrors.Add(1)
					return
				}
				totalBytes.Add(int64(chunkSize))

				// 2. Download probe
				reqID++
				payloadDw := makeTestProbePayload(wire.ProbeIperfDownload, chunkSize, 11, "")
				if err := wire.WriteMuxRequest(c, wire.ModeProbe, sid, 0, reqID, payloadDw); err != nil {
					totalErrors.Add(1)
					return
				}
				burst := wire.ProbeBurstCount(chunkSize)
				for b := 0; b < burst; b++ {
					resp, err := wire.ReadMuxResponse(c)
					if err != nil || resp.Status != wire.StatusData || resp.RequestID != reqID {
						totalErrors.Add(1)
						return
					}
					decoded := wire.DecodeMaskedResponse(resp.Status, resp.Body, sid, wire.ModeProbe, uint64(b))
					if len(decoded) != chunkSize {
						totalErrors.Add(1)
						return
					}
					totalBytes.Add(int64(len(decoded)))
				}
			}
		}(w)
	}

	wg.Wait()

	if totalErrors.Load() > 0 {
		t.Fatalf("Mux v2 sustained parallel probe encountered %d errors across 64 workers", totalErrors.Load())
	}
	if totalBytes.Load() < int64(workers*chunkSize) {
		t.Fatalf("expected total bytes >= %d, got %d", workers*chunkSize, totalBytes.Load())
	}
}

