package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
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
