package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"dragontcp/internal/cover"
	"dragontcp/internal/protocol"
	"dragontcp/internal/wire"
)

func TestParseOpenAllowsEmptyToken(t *testing.T) {
	host := "example.com"
	p := make([]byte, 6+len(host))
	binary.BigEndian.PutUint16(p[0:2], 0)
	binary.BigEndian.PutUint16(p[2:4], uint16(len(host)))
	binary.BigEndian.PutUint16(p[4:6], 443)
	copy(p[6:], host)
	token, gotHost, port, err := parseOpen(p)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" || gotHost != host || port != 443 {
		t.Fatalf("got token=%q host=%q port=%d", token, gotHost, port)
	}
}

func TestBinaryProfileProbeEndToEnd(t *testing.T) {
	for n := 0; n < 256; n += 8 {
		mask := byte(n)
		server, client := net.Pipe()
		clientResult := make(chan error, 1)
		go func() {
			defer client.Close()
			var sid wire.SessionID
			payload := make([]byte, 11)
			copy(payload[:4], wire.ProbeMagic[:])
			payload[4] = wire.ProbeKeepalive
			if err := wire.WriteRequestProfile(client, wire.ModeProbe, sid, 1, payload, mask); err != nil {
				clientResult <- err
				return
			}
			status, _, err := wire.ReadResponseProfile(client, mask)
			if err == nil && status != wire.StatusOK {
				err = fmt.Errorf("status=%d", status)
			}
			clientResult <- err
		}()

		profiled, isXOR, gotMask, err := sniffWire(server)
		if err != nil || isXOR || gotMask != mask {
			t.Fatalf("mask %02x sniff: xor=%t gotMask=%02x err=%v", mask, isXOR, gotMask, err)
		}
		req, err := wire.ReadRequestProfile(profiled, gotMask)
		if err == nil {
			err = processWireRequest(profiled, req, "", false, nil, 0, nil, 1024, 0, 0, nil)
		}
		if err != nil {
			t.Fatalf("mask %02x server: %v", mask, err)
		}
		if err := <-clientResult; err != nil {
			t.Fatalf("mask %02x client: %v", mask, err)
		}
		_ = server.Close()
	}
}

func TestXORProfileProbeEndToEnd(t *testing.T) {
	for n := 0; n < 256; n++ {
		mask := byte(n)
		if ('U'^mask)&7 < 5 {
			continue
		}
		server, client := net.Pipe()
		clientResult := make(chan error, 1)
		go func() {
			defer client.Close()
			if err := protocol.WriteRequestFrameProfile(client, 7, []byte("CPROBE -"), mask); err != nil {
				clientResult <- err
				return
			}
			id, payload, err := protocol.ReadResponseFrameProfile(client, mask)
			if err == nil && (id != 7 || string(payload) != "PROBEOK") {
				err = fmt.Errorf("id=%d payload=%q", id, payload)
			}
			clientResult <- err
		}()

		profiled, isXOR, gotMask, err := sniffWire(server)
		if err != nil || !isXOR || gotMask != mask {
			t.Fatalf("mask %02x sniff: xor=%t gotMask=%02x err=%v", mask, isXOR, gotMask, err)
		}
		handleXOR(profiled, gotMask, "", false, nil, 0, nil, 1024, 8, time.Millisecond, nil)
		if err := <-clientResult; err != nil {
			t.Fatalf("mask %02x client: %v", mask, err)
		}
		_ = server.Close()
	}
}

func TestCoveredProfilesSupportEveryHeaderMask(t *testing.T) {
	for n := 0; n < 256; n++ {
		mask := byte(n)
		for _, xor := range []bool{false, true} {
			profile := cover.Profile{
				Enabled:    true,
				ID:         uint16(mask)<<8 | uint16(mask^0xa5),
				Padding:    0,
				HeaderMask: mask,
				XOR:        xor,
				Clear:      false,
			}
			server, client := net.Pipe()
			clientResult := make(chan error, 1)
			go func() {
				defer client.Close()
				if err := cover.WritePreface(client, profile); err != nil {
					clientResult <- err
					return
				}
				if xor {
					if err := protocol.WriteRequestFrameProfile(client, 17, []byte("CPROBE -"), mask); err != nil {
						clientResult <- err
						return
					}
					id, payload, err := protocol.ReadResponseFrameProfile(client, mask)
					if err == nil && (id != 17 || string(payload) != "PROBEOK") {
						err = fmt.Errorf("id=%d payload=%q", id, payload)
					}
					clientResult <- err
					return
				}

				var sid wire.SessionID
				payload := make([]byte, 11)
				copy(payload[:4], wire.ProbeMagic[:])
				payload[4] = wire.ProbeKeepalive
				if err := wire.WriteRequestProfile(client, wire.ModeProbe, sid, 17, payload, mask); err != nil {
					clientResult <- err
					return
				}
				status, _, err := wire.ReadResponseProfile(client, mask)
				if err == nil && status != wire.StatusOK {
					err = fmt.Errorf("status=%d", status)
				}
				clientResult <- err
			}()

			profiled, gotXOR, gotMask, err := sniffWire(server)
			if err != nil || gotXOR != xor || gotMask != mask {
				t.Fatalf("mask=%02x xor=%t sniff got xor=%t mask=%02x err=%v", mask, xor, gotXOR, gotMask, err)
			}
			if xor {
				handleXOR(profiled, gotMask, "", false, nil, 0, nil, 1024, 8, time.Millisecond, nil)
			} else {
				req, readErr := wire.ReadRequestProfile(profiled, gotMask)
				if readErr == nil {
					readErr = processWireRequest(profiled, req, "", false, nil, 0, nil, 1024, 0, 0, nil)
				}
				if readErr != nil {
					t.Fatalf("mask=%02x binary server: %v", mask, readErr)
				}
			}
			if err := <-clientResult; err != nil {
				t.Fatalf("mask=%02x xor=%t client: %v", mask, xor, err)
			}
			_ = server.Close()
		}
	}
}

func TestCoveredProfilesProbeEndToEnd(t *testing.T) {
	for _, padding := range []uint16{0, 64, cover.MaxPadding} {
		for _, xor := range []bool{false, true} {
			profile := cover.Profile{Enabled: true, ID: 0x91e7, Padding: padding, HeaderMask: 0x6b, XOR: xor}
			server, client := net.Pipe()
			clientResult := make(chan error, 1)
			go func() {
				defer client.Close()
				if err := cover.WritePreface(client, profile); err != nil {
					clientResult <- err
					return
				}
				if xor {
					if err := protocol.WriteRequestFrameProfile(client, 11, []byte("CPROBE -"), profile.HeaderMask); err != nil {
						clientResult <- err
						return
					}
					id, payload, err := protocol.ReadResponseFrameProfile(client, profile.HeaderMask)
					if err == nil && (id != 11 || string(payload) != "PROBEOK") {
						err = fmt.Errorf("id=%d payload=%q", id, payload)
					}
					clientResult <- err
					return
				}

				var sid wire.SessionID
				payload := make([]byte, 11)
				copy(payload[:4], wire.ProbeMagic[:])
				payload[4] = wire.ProbeKeepalive
				if err := wire.WriteRequestProfile(client, wire.ModeProbe, sid, 3, payload, profile.HeaderMask); err != nil {
					clientResult <- err
					return
				}
				status, _, err := wire.ReadResponseProfile(client, profile.HeaderMask)
				if err == nil && status != wire.StatusOK {
					err = fmt.Errorf("status=%d", status)
				}
				clientResult <- err
			}()

			profiled, gotXOR, gotMask, err := sniffWire(server)
			if err != nil || gotXOR != xor || gotMask != profile.HeaderMask {
				t.Fatalf("padding=%d xor=%t sniff got xor=%t mask=%02x err=%v", padding, xor, gotXOR, gotMask, err)
			}
			if xor {
				handleXOR(profiled, gotMask, "", false, nil, 0, nil, 1024, 8, time.Millisecond, nil)
			} else {
				req, readErr := wire.ReadRequestProfile(profiled, gotMask)
				if readErr == nil {
					readErr = processWireRequest(profiled, req, "", false, nil, 0, nil, 1024, 0, 0, nil)
				}
				if readErr != nil {
					t.Fatalf("padding=%d binary server: %v", padding, readErr)
				}
			}
			if err := <-clientResult; err != nil {
				t.Fatalf("padding=%d xor=%t client: %v", padding, xor, err)
			}
			_ = server.Close()
		}
	}
}

func TestSniffWireRecognizesAllHeaderProfiles(t *testing.T) {
	test := func(firstTwo []byte, wantXOR bool, wantMask byte) {
		server, client := net.Pipe()
		defer server.Close()
		go func() {
			initial := make([]byte, 12)
			copy(initial, firstTwo)
			_, _ = client.Write(initial)
			_ = client.Close()
		}()

		profiled, gotXOR, gotMask, err := sniffWire(server)
		if err != nil {
			t.Fatalf("header=%x: %v", firstTwo, err)
		}
		if gotXOR != wantXOR || gotMask != wantMask {
			t.Fatalf("header=%x got xor=%t mask=%02x, want xor=%t mask=%02x", firstTwo, gotXOR, gotMask, wantXOR, wantMask)
		}
		replayed := make([]byte, 2)
		if _, err := io.ReadFull(profiled, replayed); err != nil || !bytes.Equal(replayed, firstTwo) {
			t.Fatalf("header=%x replay=%x err=%v", firstTwo, replayed, err)
		}
	}

	for n := 0; n < 256; n += 8 {
		mask := byte(n)
		test([]byte{mask, 0xa7}, false, mask)
	}
	for n := 0; n < 256; n++ {
		mask := byte(n)
		if ('U'^mask)&7 >= 5 {
			test([]byte{'U' ^ mask, 'P' ^ mask}, true, mask)
		}
	}
}

func TestStreamSessionBulkCoalescesSSHLikeBursts(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	var sid wire.SessionID
	s := newStreamSession(sid, server, "dragontcp-ssh.internal:2222", 1024*1024, 4*1024*1024, true, nil)
	defer s.close()

	const packet = 32 * 1024
	const packets = 16 // 512 KiB, matching the bulk coalescing goal.
	go func() {
		buf := make([]byte, packet)
		for i := 0; i < packets; i++ {
			for j := range buf {
				buf[j] = byte(i)
			}
			if _, err := client.Write(buf); err != nil {
				return
			}
		}
	}()

	data, status, err := s.readAt(0, 1024*1024, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if status != wire.StatusData {
		t.Fatalf("status=%d", status)
	}
	if len(data) < 512*1024 {
		t.Fatalf("bulk carrier returned only %d bytes; want at least 512 KiB", len(data))
	}
}

func TestFakeIperfProbeUploadAndDownload(t *testing.T) {
	const token = "test-token"
	const candidate = 512
	var sid wire.SessionID
	copy(sid[:], []byte("iperf-test-sid!!"))

	makePayload := func(kind byte, total int) []byte {
		base := 11 + len(token)
		if total < base {
			total = base
		}
		p := make([]byte, total)
		copy(p[:4], wire.ProbeMagic[:])
		p[4] = kind
		binary.BigEndian.PutUint16(p[5:7], uint16(len(token)))
		binary.BigEndian.PutUint32(p[7:11], candidate)
		copy(p[11:base], token)
		for i := base; i < len(p); i++ {
			p[i] = byte((i*31 + 17) & 0xff)
		}
		return p
	}

	t.Run("upload", func(t *testing.T) {
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()
		errCh := make(chan error, 1)
		go func() {
			req := wire.Request{Mode: wire.ModeProbe, Session: sid, Seq: 10, Payload: makePayload(wire.ProbeIperfUpload, candidate)}
			errCh <- processWireRequest(server, req, token, false, nil, 0, nil, 1024, 0, 0, nil)
		}()
		status, body, err := wire.ReadResponse(client)
		if err != nil {
			t.Fatal(err)
		}
		if status != wire.StatusOK || len(body) != 0 {
			t.Fatalf("upload status=%d body=%q", status, body)
		}
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	})

	t.Run("download", func(t *testing.T) {
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()
		errCh := make(chan error, 1)
		go func() {
			req := wire.Request{Mode: wire.ModeProbe, Session: sid, Seq: 20, Payload: makePayload(wire.ProbeIperfDownload, 0)}
			errCh <- processWireRequest(server, req, token, false, nil, 0, nil, 1024, 0, 0, nil)
		}()
		want := probePattern(candidate)
		for i := 0; i < wire.ProbeBurstCount(candidate); i++ {
			status, body, err := wire.ReadResponse(client)
			if err != nil {
				t.Fatal(err)
			}
			body = wire.DecodeMaskedResponse(status, body, sid, wire.ModeProbe, 20+uint64(i))
			if status != wire.StatusData || !bytes.Equal(body, want) {
				t.Fatalf("download record=%d status=%d len=%d", i, status, len(body))
			}
		}
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	})
}

func TestStreamSessionSmallWriteCoalescing(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	var sid wire.SessionID
	s := newStreamSession(sid, server, "test-target:80", 1024*1024, 4*1024*1024, false, nil)
	defer s.close()

	// Simulate an application that writes 1 byte at a time (5 bytes total, 1ms apart).
	go func() {
		for i := 0; i < 5; i++ {
			time.Sleep(1 * time.Millisecond)
			if _, err := client.Write([]byte{byte('A' + i)}); err != nil {
				return
			}
		}
	}()

	start := time.Now()
	// Long poll of 200ms with a limit of 1024 bytes.
	data, status, err := s.readAt(0, 1024, 200*time.Millisecond)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("readAt failed: %v", err)
	}
	if status != wire.StatusData {
		t.Fatalf("expected StatusData, got %d", status)
	}
	// Small writes should coalesce into a single payload, not 1 byte.
	if len(data) < 2 {
		t.Fatalf("expected coalesced payload (>=2 bytes), got %d bytes: %q", len(data), data)
	}
	// The coalescing window should return in a few ms, NOT waiting the full 200ms long-poll window.
	if elapsed >= 100*time.Millisecond {
		t.Fatalf("coalescing waited too long (%v), expected short window (<100ms)", elapsed)
	}
}

func TestStreamSessionBatchDownloadDrainsWithoutWait(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	var sid wire.SessionID
	s := newStreamSession(sid, server, "test-target:80", 1024*1024, 4*1024*1024, false, nil)
	defer s.close()

	// 1. Empty buffer with long poll should wait and return StatusWait on timeout.
	start := time.Now()
	_, status, err := s.readAt(0, 1024, 30*time.Millisecond)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("readAt empty failed: %v", err)
	}
	if status != wire.StatusWait {
		t.Fatalf("expected StatusWait, got %d", status)
	}
	if elapsed < 25*time.Millisecond {
		t.Fatalf("expected to wait close to pollWait, elapsed: %v", elapsed)
	}

	// 2. Write 100 bytes into the buffer.
	testPayload := bytes.Repeat([]byte("X"), 100)
	if _, err := client.Write(testPayload); err != nil {
		t.Fatal(err)
	}
	// Allow target reader to buffer.
	time.Sleep(10 * time.Millisecond)

	// Batch simulation: count = 3, limit = 50.
	// Record 0 (i=0): wait = 200ms. Drains first 50 bytes immediately.
	data0, status0, err := s.readAt(0, 50, 200*time.Millisecond)
	if err != nil || status0 != wire.StatusData || len(data0) != 50 {
		t.Fatalf("record 0 mismatch: status=%d len=%d err=%v", status0, len(data0), err)
	}

	// Record 1 (i=1): wait = 0 (subsequent record). Drains remaining 50 bytes immediately.
	data1, status1, err := s.readAt(50, 50, 0)
	if err != nil || status1 != wire.StatusData || len(data1) != 50 {
		t.Fatalf("record 1 mismatch: status=%d len=%d err=%v", status1, len(data1), err)
	}

	// Record 2 (i=2): wait = 0 (subsequent record). Buffer is now empty!
	// Must return StatusWait immediately WITHOUT waiting!
	startEmpty := time.Now()
	_, status2, err := s.readAt(100, 50, 0)
	drainElapsed := time.Since(startEmpty)

	if err != nil {
		t.Fatalf("record 2 error: %v", err)
	}
	if status2 != wire.StatusWait {
		t.Fatalf("expected StatusWait immediately, got %d", status2)
	}
	if drainElapsed > 10*time.Millisecond {
		t.Fatalf("subsequent record waited %v, expected immediate return (<10ms)", drainElapsed)
	}
}
