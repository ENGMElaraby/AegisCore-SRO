package vfilter

import (
	"context"
	"database/sql"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	logFlushInterval = time.Second
	logBatchSize     = 500
	logCapacity      = 50_000
)

// Event is one filter_logs row (levels/categories per the shared contract).
type Event struct {
	Level    string // debug | info | warn | error | security
	Category string // connection | login | exploit | flood | limit | restriction | command | chat | gm
	Message  string
	IP       string
	Username string
	Charname string
	Opcode   string
}

// EventLog buffers events in memory and writes them in batches; writing never blocks the game. When the
// buffer is full (database down) the oldest events are dropped and the count is logged once it recovers.
type EventLog struct {
	client  *Client
	mu      sync.Mutex
	pending []timedEvent
	dropped int
}

type timedEvent struct {
	at time.Time
	Event
}

// NewEventLog creates a log; call Run to start writing.
func NewEventLog(client *Client) *EventLog { return &EventLog{client: client} }

// Write queues an event. Safe on a nil log.
func (l *EventLog) Write(e Event) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.pending) >= logCapacity {
		l.pending = l.pending[1:]
		l.dropped++
	}
	l.pending = append(l.pending, timedEvent{at: time.Now().UTC(), Event: e})
}

// Run flushes every second until ctx ends, then once more.
func (l *EventLog) Run(ctx context.Context) {
	ticker := time.NewTicker(logFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			flushCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			l.flush(flushCtx)
			cancel()
			return
		case <-ticker.C:
			l.flush(ctx)
		}
	}
}

func (l *EventLog) flush(ctx context.Context) {
	for {
		l.mu.Lock()
		n := min(len(l.pending), logBatchSize)
		batch := append([]timedEvent(nil), l.pending[:n]...)
		dropped := l.dropped
		l.mu.Unlock()
		if n == 0 {
			return
		}
		if err := l.insert(ctx, batch); err != nil {
			if ctx.Err() == nil {
				log.WithError(err).Debug("VFILTER log write failed; keeping events")
			}
			return
		}
		l.mu.Lock()
		l.pending = l.pending[n:]
		l.dropped -= dropped
		l.mu.Unlock()
		if dropped > 0 {
			l.Write(Event{Level: "warn", Category: "connection", Message: "log events were dropped while the database was unavailable"})
		}
	}
}

func (l *EventLog) insert(ctx context.Context, batch []timedEvent) error {
	tx, err := l.client.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, e := range batch {
		_, err := tx.ExecContext(ctx, `INSERT INTO filter_logs (ts, level, module, category, ip, username, charname, opcode, message)
			VALUES (@ts, @level, @module, @category, @ip, @user, @char, @opcode, @message)`,
			sql.Named("ts", e.at), sql.Named("level", truncate(e.Level, 8)),
			sql.Named("module", truncate(l.client.Module, 32)), sql.Named("category", truncate(e.Category, 32)),
			sql.Named("ip", nullString(truncate(e.IP, 45))), sql.Named("user", nullString(truncate(e.Username, 32))),
			sql.Named("char", nullString(truncate(e.Charname, 64))), sql.Named("opcode", nullString(truncate(e.Opcode, 8))),
			sql.Named("message", truncate(e.Message, 1024)))
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
