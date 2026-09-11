package main

import (
	"fmt"
	"os"
	"sync/atomic"
	"time"
)

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
	if d == nil || !d.enabled {
		return
	}
	fmt.Fprintf(os.Stderr, "%s [DEBUG] "+format+"\n", append([]any{time.Now().Format("2006-01-02 15:04:05.000")}, args...)...)
}

func (d *serverDebug) chunkf(format string, args ...any) {
	if d == nil || !d.chunks {
		return
	}
	fmt.Fprintf(os.Stderr, "%s [CHUNK] "+format+"\n", append([]any{time.Now().Format("2006-01-02 15:04:05.000")}, args...)...)
}

func (d *serverDebug) errorf(format string, args ...any) {
	if d == nil || !d.enabled {
		return
	}
	d.errors.Add(1)
	fmt.Fprintf(os.Stderr, "%s [ERROR] "+format+"\n", append([]any{time.Now().Format("2006-01-02 15:04:05.000")}, args...)...)
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
