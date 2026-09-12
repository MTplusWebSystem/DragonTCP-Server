package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"time"

	"dragontcp/internal/wire"
)

type muxServerConn struct {
	conn         net.Conn
	headerMask   byte
	clearPayload bool
	writeMu      sync.Mutex
}

func newMuxServerConn(conn net.Conn, headerMask byte, clearPayload bool) *muxServerConn {
	return &muxServerConn{
		conn:         conn,
		headerMask:   headerMask,
		clearPayload: clearPayload,
	}
}

func (s *muxServerConn) sendResponse(status byte, reqID uint32, body []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return wire.WriteMuxResponseProfile(s.conn, status, reqID, body, s.headerMask)
}

func (s *muxServerConn) sendMaskedResponse(status byte, reqID uint32, body []byte, sid wire.SessionID, mode byte, seq uint64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return wire.WriteMaskedMuxResponseProfileEncoding(s.conn, status, reqID, body, sid, mode, seq, s.headerMask, s.clearPayload)
}

func handleMuxConnection(
	mc *muxServerConn,
	token string,
	allowPrivate bool,
	cache *dnsCache,
	tcpBuffer int,
	manager *streamManager,
	chunkMax int,
	bufferBytes int,
	pollWait time.Duration,
	debug *serverDebug,
) {
	deadline := newIdleDeadline(mc.conn, 30*time.Second)
	for {
		if deadline.refresh() != nil {
			return
		}

		req, err := wire.ReadMuxRequestProfileEncoding(mc.conn, mc.headerMask, mc.clearPayload)
		if err != nil {
			return
		}

		go handleMuxRequest(mc, req, token, allowPrivate, cache, tcpBuffer, manager, chunkMax, bufferBytes, pollWait, debug)
	}
}

func handleMuxRequest(
	mc *muxServerConn,
	req wire.MuxRequest,
	token string,
	allowPrivate bool,
	cache *dnsCache,
	tcpBuffer int,
	manager *streamManager,
	chunkMax int,
	bufferBytes int,
	pollWait time.Duration,
	debug *serverDebug,
) {
	switch req.Mode {
	case wire.ModeProbe:
		if len(req.Payload) == 0 {
			_ = mc.sendResponse(wire.StatusOK, req.RequestID, nil)
			return
		}
		kind, value, supplied, err := parseProbe(req.Payload)
		if err != nil {
			_ = mc.sendResponse(wire.StatusOK, req.RequestID, nil)
			return
		}
		if !tokenEqual(supplied, token) {
			_ = mc.sendResponse(wire.StatusError, req.RequestID, []byte("authentication failed"))
			return
		}
		switch kind {
		case wire.ProbeUpload:
			if len(req.Payload) > chunkMax {
				_ = mc.sendResponse(wire.StatusError, req.RequestID, []byte("probe too large"))
				return
			}
			_ = mc.sendResponse(wire.StatusOK, req.RequestID, nil)
		case wire.ProbeDownload:
			if value < 1 || value > chunkMax {
				_ = mc.sendResponse(wire.StatusError, req.RequestID, []byte("probe too large"))
				return
			}
			_ = mc.sendMaskedResponse(wire.StatusData, req.RequestID, probePattern(value), req.Session, wire.ModeProbe, req.Seq)
		case wire.ProbeKeepalive:
			_ = mc.sendResponse(wire.StatusOK, req.RequestID, nil)
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
				if err := mc.sendMaskedResponse(wire.StatusData, req.RequestID, data, req.Session, wire.ModeProbe, req.Seq+uint64(i)); err != nil {
					return
				}
			}
		case wire.ProbeIperfUpload:
			if value < 1 || value > chunkMax || len(req.Payload) > chunkMax {
				_ = mc.sendResponse(wire.StatusError, req.RequestID, []byte("iperf upload chunk too large"))
				return
			}
			if !validateIperfUploadPayload(req.Payload, supplied, value) {
				_ = mc.sendResponse(wire.StatusError, req.RequestID, []byte("iperf upload validation failed"))
				return
			}
			if debug != nil && debug.enabled {
				workers := calculateParallelWorkers(value)
				debug.logf("CALIBRATION fake_iperf=upload mux=true peer=%s chunk=%d bytes=%d seq=%d pollers=%d outstanding=%d", mc.conn.RemoteAddr(), value, len(req.Payload), req.Seq, workers, workers)
			}
			_ = mc.sendResponse(wire.StatusOK, req.RequestID, nil)
		case wire.ProbeIperfDownload:
			if value < 1 || value > chunkMax {
				_ = mc.sendResponse(wire.StatusError, req.RequestID, []byte("iperf download chunk too large"))
				return
			}
			count := wire.ProbeBurstCount(value)
			if debug != nil && debug.enabled {
				workers := calculateParallelWorkers(value)
				debug.logf("CALIBRATION fake_iperf=download mux=true peer=%s chunk=%d records=%d bytes=%d pollers=%d outstanding=%d", mc.conn.RemoteAddr(), value, count, value*count, workers, workers)
			}
			data := probePattern(value)
			for i := 0; i < count; i++ {
				if err := mc.sendMaskedResponse(wire.StatusData, req.RequestID, data, req.Session, wire.ModeProbe, req.Seq+uint64(i)); err != nil {
					return
				}
			}
		default:
			_ = mc.sendResponse(wire.StatusOK, req.RequestID, nil)
		}

	case wire.ModeOpen:
		supplied, host, port, err := parseOpen(req.Payload)
		if err != nil {
			_ = mc.sendResponse(wire.StatusError, req.RequestID, []byte(err.Error()))
			return
		}
		if !tokenEqual(supplied, token) {
			_ = mc.sendResponse(wire.StatusError, req.RequestID, []byte("authentication failed"))
			return
		}
		if old := manager.get(req.Session); old != nil {
			var body [4]byte
			binary.BigEndian.PutUint32(body[:], uint32(chunkMax))
			_ = mc.sendResponse(wire.StatusOK, req.RequestID, body[:])
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		target, err := dialTarget(ctx, host, port, allowPrivate, cache, tcpBuffer)
		cancel()
		if err != nil {
			_ = mc.sendResponse(wire.StatusError, req.RequestID, []byte(err.Error()))
			return
		}
		_, internalCarrier := lookupInternalTarget(host, port)
		session := newStreamSession(req.Session, target, fmt.Sprintf("%s:%d", host, port), chunkMax, bufferBytes, internalCarrier, debug)
		_, created := manager.addOrGet(req.Session, session)
		if created && debug != nil && debug.enabled {
			debug.sessionsOpened.Add(1)
			debug.activeSessions.Add(1)
			debug.logf("SESSION OPEN sid=%x target=%s:%d active_sessions=%d mux=true", req.Session[:4], host, port, manager.count())
		}
		var body [4]byte
		binary.BigEndian.PutUint32(body[:], uint32(chunkMax))
		_ = mc.sendResponse(wire.StatusOK, req.RequestID, body[:])

	case wire.ModeUpload:
		s := manager.get(req.Session)
		if s == nil {
			_ = mc.sendResponse(wire.StatusError, req.RequestID, []byte("unknown session"))
			return
		}
		if len(req.Payload) > chunkMax {
			_ = mc.sendResponse(wire.StatusError, req.RequestID, []byte("upload too large"))
			return
		}
		if err := s.upload(req.Seq, req.Payload); err != nil {
			_ = mc.sendResponse(wire.StatusError, req.RequestID, []byte(err.Error()))
			return
		}
		_ = mc.sendResponse(wire.StatusOK, req.RequestID, nil)

	case wire.ModeDownload:
		s := manager.get(req.Session)
		if s == nil {
			_ = mc.sendResponse(wire.StatusError, req.RequestID, []byte("unknown session"))
			return
		}
		limit := chunkMax
		count := 1
		offset := req.Seq
		if len(req.Payload) >= 14 {
			ack := binary.BigEndian.Uint64(req.Payload[0:8])
			limit = int(binary.BigEndian.Uint32(req.Payload[8:12]))
			count = int(binary.BigEndian.Uint16(req.Payload[12:14]))
			s.ack(ack)
		} else if len(req.Payload) >= 6 {
			limit = int(binary.BigEndian.Uint32(req.Payload[0:4]))
			count = int(binary.BigEndian.Uint16(req.Payload[4:6]))
			s.ack(offset)
		} else {
			s.ack(offset)
		}

		if limit < 1 {
			limit = 1
		}
		if limit > chunkMax {
			limit = chunkMax
		}
		if count < 1 {
			count = 1
		}
		if count > 256 {
			count = 256
		}

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
				_ = mc.sendResponse(wire.StatusError, req.RequestID, []byte(err.Error()))
				return
			}
			switch status {
			case wire.StatusData:
				if debug != nil && debug.enabled {
					debug.dataRecords.Add(1)
				}
				if len(data) > 0 {
					if err := mc.sendMaskedResponse(wire.StatusData, req.RequestID, data, req.Session, wire.ModeDownload, offset); err != nil {
						return
					}
					offset += uint64(len(data))
				}
			case wire.StatusWait:
				if debug != nil && debug.enabled {
					debug.waitRecords.Add(1)
				}
				_ = mc.sendResponse(wire.StatusWait, req.RequestID, nil)
				return
			case wire.StatusEOF:
				_ = mc.sendResponse(wire.StatusEOF, req.RequestID, nil)
				return
			default:
				_ = mc.sendResponse(wire.StatusError, req.RequestID, []byte("invalid session read status"))
				return
			}
		}
		_ = mc.sendResponse(wire.StatusWait, req.RequestID, nil)

	case wire.ModeClose:
		manager.remove(req.Session)
		_ = mc.sendResponse(wire.StatusOK, req.RequestID, nil)

	default:
		_ = mc.sendResponse(wire.StatusError, req.RequestID, []byte("unknown mode"))
	}
}
