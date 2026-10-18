package vfilter

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	// CommandPollInterval keeps the panel's "pending → done" under a few seconds.
	CommandPollInterval = time.Second
	// CommandExpiry: a command claimed later than this after it was queued is failed, not run (contract 0.3.3).
	CommandExpiry   = 10 * time.Minute
	maxResultLength = 500
	pendingBatch    = 20
)

// Command is one claimed filter_commands row.
type Command struct {
	ID        int64
	Name      string
	Target    string
	Payload   json.RawMessage
	CreatedAt time.Time
	DBNow     time.Time
}

// PayloadString reads a string field of the JSON payload ("" when absent or not a string).
func (c Command) PayloadString(field string) string {
	var values map[string]any
	if json.Unmarshal(c.Payload, &values) != nil {
		return ""
	}
	value, _ := values[field].(string)
	return value
}

// Outcome is how a command ended.
type Outcome struct {
	OK     bool
	Result string
}

// Done and Failed build outcomes.
func Done(result string) Outcome   { return Outcome{OK: true, Result: result} }
func Failed(result string) Outcome { return Outcome{OK: false, Result: result} }

// Handler carries out one claimed command.
type Handler func(ctx context.Context, command Command) Outcome

// RunCommands polls for pending commands named in owned, claims each with the contract's conditional update
// (pending → running; a cancelled or already taken command is skipped) and records the outcome. Commands queued
// more than CommandExpiry before the claim fail as "expired" without running.
func (c *Client) RunCommands(ctx context.Context, owned []string, handle Handler) {
	ticker := time.NewTicker(CommandPollInterval)
	defer ticker.Stop()
	failing := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if err := c.processPending(ctx, owned, handle); err != nil {
			if !failing && ctx.Err() == nil {
				log.WithError(err).Warn("VFILTER command queue unavailable")
			}
			failing = true
			continue
		}
		failing = false
	}
}

func (c *Client) processPending(ctx context.Context, owned []string, handle Handler) error {
	ids, err := c.pendingIDs(ctx, owned)
	if err != nil {
		return err
	}
	for _, id := range ids {
		command, err := c.claim(ctx, id)
		if err != nil {
			return err
		}
		if command == nil {
			continue
		}

		var outcome Outcome
		if command.DBNow.Sub(command.CreatedAt) > CommandExpiry {
			outcome = Failed("expired")
		} else {
			outcome = safeHandle(ctx, handle, *command)
		}
		if err := c.finish(ctx, command.ID, outcome); err != nil {
			return err
		}
		log.WithFields(log.Fields{"command": command.Name, "target": command.Target, "ok": outcome.OK}).Info("VFILTER command: " + outcome.Result)
	}
	return nil
}

func safeHandle(ctx context.Context, handle Handler, command Command) (outcome Outcome) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.WithField("command", command.Name).Errorf("VFILTER command panicked: %v", recovered)
			outcome = Failed("internal error")
		}
	}()
	return handle(ctx, command)
}

func (c *Client) pendingIDs(ctx context.Context, owned []string) ([]int64, error) {
	if len(owned) == 0 {
		return nil, nil
	}
	rows, err := c.db.QueryContext(ctx, `SELECT TOP (@limit) id FROM filter_commands
		WHERE status = 'pending' AND command IN (SELECT value FROM STRING_SPLIT(@owned, ','))
		ORDER BY created_at, id`,
		sql.Named("limit", pendingBatch), sql.Named("owned", strings.Join(owned, ",")))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// claim moves one command from pending to running. It returns nil when the website cancelled it or another
// process took it first (contract 0.3.0: a command is run or cancelled, never both).
func (c *Client) claim(ctx context.Context, id int64) (*Command, error) {
	var command Command
	var payload sql.NullString
	err := c.db.QueryRowContext(ctx, `UPDATE filter_commands SET status = 'running'
		OUTPUT inserted.id, inserted.command, inserted.target, inserted.payload, inserted.created_at, SYSUTCDATETIME()
		WHERE id = @id AND status = 'pending'`, sql.Named("id", id)).
		Scan(&command.ID, &command.Name, &command.Target, &payload, &command.CreatedAt, &command.DBNow)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if payload.Valid {
		command.Payload = json.RawMessage(payload.String)
	}
	return &command, nil
}

func (c *Client) finish(ctx context.Context, id int64, outcome Outcome) error {
	status := "failed"
	if outcome.OK {
		status = "done"
	}
	result := outcome.Result
	if len(result) > maxResultLength {
		result = result[:maxResultLength-3] + "..."
	}
	_, err := c.db.ExecContext(ctx, `UPDATE filter_commands SET status = @status, result = @result, processed_at = SYSUTCDATETIME()
		WHERE id = @id AND status = 'running'`,
		sql.Named("status", status), sql.Named("result", result), sql.Named("id", id))
	return err
}
