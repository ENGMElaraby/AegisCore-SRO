package vfilter

import (
	"context"
	"database/sql"
	"time"

	log "github.com/sirupsen/logrus"
)

// HeartbeatInterval: the website shows a module online while its heartbeat is under 15 s old.
const HeartbeatInterval = 5 * time.Second

// Status is one filter_status row. Times are written with the database clock.
type Status struct {
	Module      string
	State       string // stopped | starting | running | error
	OnlineCount int
	Connections int
	GatewayMode string // gateway only; "" stores NULL
	Version     string
	Uptime      time.Duration // 0 stores NULL started_at
	LastError   string
}

// WriteStatus upserts the row with heartbeat_at = database now.
func (c *Client) WriteStatus(ctx context.Context, s Status) error {
	_, err := c.db.ExecContext(ctx, `MERGE filter_status WITH (HOLDLOCK) AS t
		USING (SELECT @module AS module) AS s ON t.module = s.module
		WHEN MATCHED THEN UPDATE SET state = @state, online_count = @online, connections = @connections,
			gateway_mode = @mode, version = @version, last_error = @error, heartbeat_at = SYSUTCDATETIME(),
			started_at = CASE WHEN @uptime IS NULL THEN NULL ELSE DATEADD(second, -@uptime, SYSUTCDATETIME()) END
		WHEN NOT MATCHED THEN INSERT (module, state, online_count, connections, gateway_mode, version, started_at, heartbeat_at, last_error)
			VALUES (@module, @state, @online, @connections, @mode, @version,
				CASE WHEN @uptime IS NULL THEN NULL ELSE DATEADD(second, -@uptime, SYSUTCDATETIME()) END, SYSUTCDATETIME(), @error);`,
		sql.Named("module", s.Module),
		sql.Named("state", s.State),
		sql.Named("online", s.OnlineCount),
		sql.Named("connections", s.Connections),
		sql.Named("mode", nullString(s.GatewayMode)),
		sql.Named("version", nullString(s.Version)),
		sql.Named("error", nullString(s.LastError)),
		sql.Named("uptime", nullSeconds(s.Uptime)))
	return err
}

// RunHeartbeat writes current() every HeartbeatInterval until ctx ends, then once more with state "stopped".
func (c *Client) RunHeartbeat(ctx context.Context, current func() Status) {
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()
	failing := false
	for {
		if err := c.WriteStatus(ctx, current()); err != nil && ctx.Err() == nil {
			if !failing {
				log.WithError(err).Warn("VFILTER heartbeat could not be written")
			}
			failing = true
		} else {
			failing = false
		}
		select {
		case <-ctx.Done():
			final := current()
			final.State, final.Uptime = "stopped", 0
			stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = c.WriteStatus(stopCtx, final)
			cancel()
			return
		case <-ticker.C:
		}
	}
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullSeconds(d time.Duration) any {
	if d <= 0 {
		return nil
	}
	return int(d / time.Second)
}
