// Package vfilter implements the AegisCore side of the VFILTER shared contract (VFilter docs/shared-contract.md
// 0.3.3): the VFilter control panel configures and controls the server through a SQL Server database. See
// _docs/vfilter-integration.md. With VFILTER_DSN unset, Open returns (nil, nil) and every caller treats a nil
// *Client as "integration off".
package vfilter

import (
	"context"
	"database/sql"
	"fmt"
	"runtime/debug"
	"time"

	_ "github.com/microsoft/go-mssqldb"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

const (
	dsnKey        = "vfilter.dsn"
	moduleNameKey = "vfilter.module_name"
	pingTimeout   = 5 * time.Second
	maxOpenConns  = 4
)

// Client is one process's connection to the VFILTER database.
type Client struct {
	db     *sql.DB
	Module string
}

// Open connects when VFILTER_DSN is set. defaultModule names this process's filter_status row
// (overridable with VFILTER_MODULE_NAME).
func Open(ctx context.Context, defaultModule string) (*Client, error) {
	_ = viper.BindEnv(dsnKey, "VFILTER_DSN")
	_ = viper.BindEnv(moduleNameKey, "VFILTER_MODULE_NAME")
	dsn := viper.GetString(dsnKey)
	if dsn == "" {
		return nil, nil
	}

	module := viper.GetString(moduleNameKey)
	if module == "" {
		module = defaultModule
	}

	// A server must not lose its VFILTER rules because SQL Server was down at boot: keep the pool (database/sql
	// reconnects on its own) and let the pollers retry. Only an unusable DSN is fatal.
	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return nil, fmt.Errorf("open VFILTER database: invalid configuration")
	}
	db.SetMaxOpenConns(maxOpenConns)
	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		log.WithError(err).Warn("VFILTER database not reachable yet; will keep retrying")
	}
	return &Client{db: db, Module: module}, nil
}

// BuildVersion is reported in filter_status.version: "aegiscore" plus the short VCS revision when known.
func BuildVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && len(setting.Value) >= 7 {
				return "aegiscore-" + setting.Value[:7]
			}
		}
	}
	return "aegiscore"
}

// OpenDSN connects with an explicit DSN (tests and the supervisor).
func OpenDSN(ctx context.Context, dsn, module string) (*Client, error) {
	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		// The DSN may contain a password: never include it in errors.
		return nil, fmt.Errorf("open VFILTER database: invalid configuration")
	}
	db.SetMaxOpenConns(maxOpenConns)

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect VFILTER database: %w", err)
	}

	return &Client{db: db, Module: module}, nil
}

// Close releases the pool. Safe on a nil client.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	return c.db.Close()
}

// DB exposes the pool for package-internal helpers and tests.
func (c *Client) DB() *sql.DB { return c.db }
