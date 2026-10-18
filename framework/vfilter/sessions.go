package vfilter

import (
	"context"
	"database/sql"
	"sync"

	log "github.com/sirupsen/logrus"
)

const sessionQueueSize = 4096

// SessionInfo identifies one in-world player for filter_sessions.
type SessionInfo struct {
	Key      string // AegisCore session ID
	IP       string
	Username string
	Charname string
}

type sessionChange struct {
	open bool
	info SessionInfo
}

// SessionMirror keeps filter_sessions in step with this process's players. Opened/Closed never block the
// game: changes go through a bounded queue and a background writer (dropped, with a warning, if it is full).
type SessionMirror struct {
	client  *Client
	changes chan sessionChange
	mu      sync.Mutex
	rows    map[string]int64
}

// NewSessionMirror creates a mirror; call Run to start writing.
func NewSessionMirror(client *Client) *SessionMirror {
	return &SessionMirror{client: client, changes: make(chan sessionChange, sessionQueueSize), rows: map[string]int64{}}
}

// Opened records a player who entered the world.
func (m *SessionMirror) Opened(info SessionInfo) { m.enqueue(sessionChange{open: true, info: info}) }

// Closed removes a player's row.
func (m *SessionMirror) Closed(key string) { m.enqueue(sessionChange{info: SessionInfo{Key: key}}) }

func (m *SessionMirror) enqueue(change sessionChange) {
	if m == nil {
		return
	}
	select {
	case m.changes <- change:
	default:
		log.Warn("VFILTER session mirror queue full; change dropped")
	}
}

// Run clears this module's rows from a previous run, then applies changes until ctx ends.
func (m *SessionMirror) Run(ctx context.Context) {
	if _, err := m.client.db.ExecContext(ctx, "DELETE FROM filter_sessions WHERE module = @module", sql.Named("module", m.client.Module)); err != nil {
		log.WithError(err).Warn("VFILTER could not clear old sessions")
	}
	for {
		select {
		case <-ctx.Done():
			return
		case change := <-m.changes:
			if err := m.apply(ctx, change); err != nil && ctx.Err() == nil {
				log.WithError(err).Warn("VFILTER session mirror write failed")
			}
		}
	}
}

func (m *SessionMirror) apply(ctx context.Context, change sessionChange) error {
	m.mu.Lock()
	id, known := m.rows[change.info.Key]
	m.mu.Unlock()

	if !change.open {
		if !known {
			return nil
		}
		m.mu.Lock()
		delete(m.rows, change.info.Key)
		m.mu.Unlock()
		_, err := m.client.db.ExecContext(ctx, "DELETE FROM filter_sessions WHERE id = @id", sql.Named("id", id))
		return err
	}

	if known {
		_, err := m.client.db.ExecContext(ctx, "UPDATE filter_sessions SET username = @user, charname = @char, last_packet_at = SYSUTCDATETIME() WHERE id = @id",
			sql.Named("user", nullString(truncate(change.info.Username, 32))), sql.Named("char", nullString(truncate(change.info.Charname, 64))), sql.Named("id", id))
		return err
	}

	err := m.client.db.QueryRowContext(ctx, `INSERT INTO filter_sessions (module, ip, username, charname, connected_at, last_packet_at)
		OUTPUT inserted.id VALUES (@module, @ip, @user, @char, SYSUTCDATETIME(), SYSUTCDATETIME())`,
		sql.Named("module", m.client.Module), sql.Named("ip", truncate(change.info.IP, 45)),
		sql.Named("user", nullString(truncate(change.info.Username, 32))), sql.Named("char", nullString(truncate(change.info.Charname, 64)))).Scan(&id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.rows[change.info.Key] = id
	m.mu.Unlock()
	return nil
}

func truncate(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}
