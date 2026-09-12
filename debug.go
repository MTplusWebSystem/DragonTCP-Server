package main

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type logRing struct {
	mu    sync.RWMutex
	lines []string
	max   int
}

func newLogRing(max int) *logRing {
	return &logRing{max: max}
}

func (r *logRing) add(line string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.lines) >= r.max {
		r.lines = r.lines[1:]
	}
	r.lines = append(r.lines, line)
}

func (r *logRing) get(n int) []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if n <= 0 || n > len(r.lines) {
		n = len(r.lines)
	}
	start := len(r.lines) - n
	out := make([]string, n)
	copy(out, r.lines[start:])
	return out
}

var globalLogRing = newLogRing(500)

type serverDebug struct {
	enabled    bool
	chunks     bool
	statsEvery time.Duration
	started    time.Time

	sessionsOpened atomic.Uint64
	sessionsClosed atomic.Uint64
	activeSessions atomic.Int64
	bytesUp        atomic.Uint64
	bytesDown      atomic.Uint64
	pushRecords    atomic.Uint64
	pullRequests   atomic.Uint64
	dataRecords    atomic.Uint64
	waitRecords    atomic.Uint64
	errors         atomic.Uint64
}

func newServerDebug(enabled, chunks bool, statsEvery time.Duration) *serverDebug {
	d := &serverDebug{
		enabled:    enabled || chunks,
		chunks:     chunks,
		statsEvery: statsEvery,
		started:    time.Now(),
	}
	if d.enabled && d.statsEvery > 0 {
		go d.statsLoop()
	}
	return d
}

func (d *serverDebug) logf(format string, args ...any) {
	msg := fmt.Sprintf("%s [DEBUG] "+format, append([]any{time.Now().Format("2006-01-02 15:04:05.000")}, args...)...)
	globalLogRing.add(msg)
	if d == nil || !d.enabled {
		return
	}
	fmt.Fprintln(os.Stderr, msg)
}

func (d *serverDebug) chunkf(format string, args ...any) {
	msg := fmt.Sprintf("%s [CHUNK] "+format, append([]any{time.Now().Format("2006-01-02 15:04:05.000")}, args...)...)
	globalLogRing.add(msg)
	if d == nil || !d.chunks {
		return
	}
	fmt.Fprintln(os.Stderr, msg)
}

func (d *serverDebug) errorf(format string, args ...any) {
	if d != nil {
		d.errors.Add(1)
	}
	msg := fmt.Sprintf("%s [ERROR] "+format, append([]any{time.Now().Format("2006-01-02 15:04:05.000")}, args...)...)
	globalLogRing.add(msg)
	if d == nil || !d.enabled {
		return
	}
	fmt.Fprintln(os.Stderr, msg)
}

func (d *serverDebug) statsLoop() {
	ticker := time.NewTicker(d.statsEvery)
	defer ticker.Stop()
	for range ticker.C {
		d.logf(
			"STATS uptime=%s active_connections=%d active_sessions=%d sessions_opened=%d sessions_closed=%d bytes_up=%d bytes_down=%d push_records=%d pull_requests=%d data_records=%d waits=%d errors=%d",
			time.Since(d.started).Round(time.Second),
			atomic.LoadInt64(&active),
			d.activeSessions.Load(),
			d.sessionsOpened.Load(),
			d.sessionsClosed.Load(),
			d.bytesUp.Load(),
			d.bytesDown.Load(),
			d.pushRecords.Load(),
			d.pullRequests.Load(),
			d.dataRecords.Load(),
			d.waitRecords.Load(),
			d.errors.Load(),
		)
	}
}
