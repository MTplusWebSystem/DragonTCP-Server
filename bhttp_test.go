package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"dragontcp/internal/cover"
	"dragontcp/internal/wire"
)

func writeBHTTPTestRequest(w io.Writer, mode byte, sid wire.SessionID, seq uint64, payload []byte, downloadHint uint32) error {
	n := uint32(len(payload))
	if mode == bhttpModeDownload {
		n = downloadHint
		payload = nil
	}
	packet := make([]byte, bhttpRequestHeaderSize+len(payload))
	packet[0] = mode
	copy(packet[1:17], sid[:])
	binary.BigEndian.PutUint64(packet[17:25], seq)
	binary.BigEndian.PutUint32(packet[25:29], n)
	copy(packet[29:], payload)
	wire.MaskInPlace(packet[29:], sid, mode, seq, false)
	_, err := w.Write(packet)
	return err
}

func readBHTTPTestResponse(r io.Reader, sid wire.SessionID, mode byte, seq uint64) (byte, []byte, error) {
	status, body, err := wire.ReadResponse(r)
	if err == nil && status != wire.StatusError {
		wire.MaskInPlace(body, sid, mode, seq, true)
	}
	return status, body, err
}

func startBHTTPTestServer(t *testing.T, sessions *bhttpSessionManager) (net.Conn, <-chan struct{}) {
	t.Helper()
	server, client := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer server.Close()
		handleBinary(
			server,
			0,
			false,
			"",
			false,
			newDNSCache(time.Minute, 16),
			0,
			newStreamManager(time.Minute, nil),
			sessions,
			1024*1024,
			1024*1024,
			10*time.Millisecond,
			nil,
		)
	}()
	return client, done
}

func TestBHTTPReferenceSessionStack(t *testing.T) {
	sessions := newBHTTPSessionManager(time.Minute, 32)
	client, done := startBHTTPTestServer(t, sessions)
	defer func() {
		client.Close()
		<-done
	}()
	_ = client.SetDeadline(time.Now().Add(2 * time.Second))

	var sid wire.SessionID
	for i := range sid {
		sid[i] = byte(i + 1)
	}

	if err := writeBHTTPTestRequest(client, bhttpModeUpload, sid, 0, nil, 0); err != nil {
		t.Fatal(err)
	}
	if status, _, err := readBHTTPTestResponse(client, sid, bhttpModeUpload, 0); err != nil || status != wire.StatusOK {
		t.Fatalf("registration status=%d err=%v", status, err)
	}

	if err := writeBHTTPTestRequest(client, bhttpModeUpload, sid, 1, []byte("Hello BHTTP"), 0); err != nil {
		t.Fatal(err)
	}
	if status, _, err := readBHTTPTestResponse(client, sid, bhttpModeUpload, 1); err != nil || status != wire.StatusOK {
		t.Fatalf("upload status=%d err=%v", status, err)
	}

	// The size is in the header but no 1,350-byte body follows. This is the
	// framing difference that made the native Dragon parser wait forever.
	if err := writeBHTTPTestRequest(client, bhttpModeDownload, sid, 0, nil, 1350); err != nil {
		t.Fatal(err)
	}
	if status, _, err := readBHTTPTestResponse(client, sid, bhttpModeDownload, 0); err != nil || status != wire.StatusOK {
		t.Fatalf("download status=%d err=%v", status, err)
	}

	batch := make([]byte, 6)
	binary.BigEndian.PutUint32(batch[:4], 1350)
	binary.BigEndian.PutUint16(batch[4:], 2)
	if err := writeBHTTPTestRequest(client, bhttpModeBatchDownload, sid, 0, batch, 0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if status, _, err := readBHTTPTestResponse(client, sid, bhttpModeBatchDownload, 0); err != nil || status != wire.StatusOK {
			t.Fatalf("batch response %d status=%d err=%v", i, status, err)
		}
	}

	if err := writeBHTTPTestRequest(client, bhttpModeACK, sid, 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	if status, _, err := readBHTTPTestResponse(client, sid, bhttpModeACK, 5); err != nil || status != wire.StatusOK {
		t.Fatalf("ack status=%d err=%v", status, err)
	}
}

func TestBPExplicitCloseRemovesSession(t *testing.T) {
	sessions := newBHTTPSessionManager(time.Minute, 32)
	client, done := startBHTTPTestServer(t, sessions)
	defer func() {
		client.Close()
		<-done
	}()
	_ = client.SetDeadline(time.Now().Add(2 * time.Second))

	var sid wire.SessionID
	copy(sid[:], []byte("close-session-01"))
	if err := writeBHTTPTestRequest(client, bhttpModeUpload, sid, 0, nil, 0); err != nil {
		t.Fatal(err)
	}
	if status, _, err := readBHTTPTestResponse(client, sid, bhttpModeUpload, 0); err != nil || status != wire.StatusOK {
		t.Fatalf("registration status=%d err=%v", status, err)
	}
	if sessions.get(sid) == nil {
		t.Fatal("registered session is missing")
	}

	if err := writeBHTTPTestRequest(client, bhttpModeACK, sid, 0, bpCloseMagic[:], 0); err != nil {
		t.Fatal(err)
	}
	if status, _, err := readBHTTPTestResponse(client, sid, bhttpModeACK, 0); err != nil || status != wire.StatusOK {
		t.Fatalf("close status=%d err=%v", status, err)
	}
	if sessions.get(sid) != nil {
		t.Fatal("explicit close retained the session")
	}
}

func TestBHTTPReferenceProbeAndBatchEcho(t *testing.T) {
	sessions := newBHTTPSessionManager(time.Minute, 32)
	client, done := startBHTTPTestServer(t, sessions)
	defer func() {
		client.Close()
		<-done
	}()
	_ = client.SetDeadline(time.Now().Add(2 * time.Second))

	var sid wire.SessionID
	copy(sid[:], []byte("probe-session-01"))
	payload := make([]byte, 10)
	copy(payload[:4], []byte("BHP1"))
	payload[4] = 1
	payload[5] = bhttpModeDownload
	binary.BigEndian.PutUint32(payload[6:], 512)
	if err := writeBHTTPTestRequest(client, bhttpModeProbe, sid, 0, payload, 0); err != nil {
		t.Fatal(err)
	}
	status, body, err := readBHTTPTestResponse(client, sid, bhttpModeProbe, 0)
	if err != nil || status != wire.StatusOK || len(body) != 512 || !bytes.Equal(body[:10], payload) {
		t.Fatalf("download probe status=%d len=%d err=%v", status, len(body), err)
	}
	for i := 10; i < len(body); i++ {
		if body[i] != byte(i*31) {
			t.Fatalf("probe pattern byte %d=%02x", i, body[i])
		}
	}

	payload[5] = bhttpModeACK
	binary.BigEndian.PutUint32(payload[6:], 3)
	if err := writeBHTTPTestRequest(client, bhttpModeProbe, sid, 0, payload, 0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		status, body, err := readBHTTPTestResponse(client, sid, bhttpModeProbe, 0)
		if err != nil || status != wire.StatusOK || !bytes.Equal(body, payload) {
			t.Fatalf("batch probe %d status=%d body=%x err=%v", i, status, body, err)
		}
	}
}

func TestBHTTPUnknownSessionDownloadHasNoBody(t *testing.T) {
	sessions := newBHTTPSessionManager(time.Minute, 32)
	client, done := startBHTTPTestServer(t, sessions)
	defer func() {
		client.Close()
		<-done
	}()
	_ = client.SetDeadline(time.Now().Add(2 * time.Second))

	var sid wire.SessionID
	copy(sid[:], []byte("unknown-session!"))
	if err := writeBHTTPTestRequest(client, bhttpModeDownload, sid, 0, nil, 1350); err != nil {
		t.Fatal(err)
	}
	status, _, err := wire.ReadResponse(client)
	if err != nil || status == wire.StatusOK || status == wire.StatusData {
		t.Fatalf("unknown session status=%d err=%v", status, err)
	}
}

func TestBinaryAutoDetectionKeepsNativeDragonProbe(t *testing.T) {
	sessions := newBHTTPSessionManager(time.Minute, 32)
	client, done := startBHTTPTestServer(t, sessions)
	defer func() {
		client.Close()
		<-done
	}()
	_ = client.SetDeadline(time.Now().Add(2 * time.Second))

	var sid wire.SessionID
	payload := make([]byte, 11)
	copy(payload[:4], wire.ProbeMagic[:])
	payload[4] = wire.ProbeKeepalive
	if err := wire.WriteRequest(client, wire.ModeProbe, sid, 1, payload); err != nil {
		t.Fatal(err)
	}
	status, _, err := wire.ReadResponse(client)
	if err != nil || status != wire.StatusOK {
		t.Fatalf("native probe status=%d err=%v", status, err)
	}
}

func TestClearCoveredBinaryAndBPProfiles(t *testing.T) {
	for _, bp := range []bool{false, true} {
		t.Run(map[bool]string{false: "B", true: "BP"}[bp], func(t *testing.T) {
			server, client := net.Pipe()
			profile := cover.Profile{Enabled: true, ID: 0x8173, Padding: 32, HeaderMask: 0x9b, Clear: true}
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer server.Close()
				profiled, isXOR, mask, err := sniffWire(server)
				if err != nil || isXOR {
					return
				}
				handleBinary(
					profiled, mask, true, "", false,
					newDNSCache(time.Minute, 16), 0,
					newStreamManager(time.Minute, nil),
					newBHTTPSessionManager(time.Minute, 32),
					1024*1024, 1024*1024, 10*time.Millisecond, nil,
				)
			}()
			defer func() {
				client.Close()
				<-done
			}()
			_ = client.SetDeadline(time.Now().Add(2 * time.Second))
			if err := cover.WritePreface(client, profile); err != nil {
				t.Fatal(err)
			}

			var sid wire.SessionID
			copy(sid[:], []byte("clear-profile-01"))
			if bp {
				payload := makeBHTTPProbe(bhttpModeDownload, 256)[:10]
				packet := make([]byte, bhttpRequestHeaderSize+len(payload))
				packet[0] = bhttpModeProbe ^ profile.HeaderMask
				copy(packet[1:17], sid[:])
				binary.BigEndian.PutUint32(packet[25:29], uint32(len(payload)))
				copy(packet[29:], payload)
				if _, err := client.Write(packet); err != nil {
					t.Fatal(err)
				}
				status, body, err := wire.ReadResponseProfile(client, profile.HeaderMask)
				if err != nil || status != wire.StatusOK || !bytes.Equal(body, makeBHTTPProbe(bhttpModeDownload, 256)) {
					t.Fatalf("clear BP status=%d len=%d err=%v", status, len(body), err)
				}
				return
			}

			payload := make([]byte, 11)
			copy(payload[:4], wire.ProbeMagic[:])
			payload[4] = wire.ProbeDownload
			binary.BigEndian.PutUint32(payload[7:11], 256)
			if err := wire.WriteRequestProfileEncoding(client, wire.ModeProbe, sid, 7, payload, profile.HeaderMask, true); err != nil {
				t.Fatal(err)
			}
			status, body, err := wire.ReadResponseProfile(client, profile.HeaderMask)
			if err != nil || status != wire.StatusData || !bytes.Equal(body, probePattern(256)) {
				t.Fatalf("clear B status=%d len=%d err=%v", status, len(body), err)
			}
		})
	}
}
