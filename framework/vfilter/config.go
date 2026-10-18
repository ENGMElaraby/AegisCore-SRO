package vfilter

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

// ConfigPollInterval is how often the change counters are read (contract rule 4).
const ConfigPollInterval = 2 * time.Second

// Module is one filter_modules row.
type Module struct {
	Name       string
	Type       string
	ModuleIP   string
	ModulePort int
	Enabled    bool
}

// Target is the command target naming this module (lower-case name, contract 0.3.1).
func (m Module) Target() string { return strings.ToLower(m.Name) }

// Snapshot is a read-only view of the configuration areas AegisCore uses. It is replaced whole on reload.
type Snapshot struct {
	Settings map[string]string
	Lists    map[string][]string
	Modules  []Module
}

// Int returns a setting as an int, or fallback when missing or not a number.
func (s *Snapshot) Int(key string, fallback int) int {
	if value, err := strconv.Atoi(s.Settings[key]); err == nil {
		return value
	}
	return fallback
}

// Bool returns a bool setting (stored "1"/"0").
func (s *Snapshot) Bool(key string, fallback bool) bool {
	switch s.Settings[key] {
	case "1", "true":
		return true
	case "0", "false":
		return false
	default:
		return fallback
	}
}

// Config keeps the snapshot current by polling filter_config_version and reloading changed areas.
type Config struct {
	client   *Client
	current  atomic.Pointer[Snapshot]
	mu       sync.Mutex
	versions map[string]int64
}

// The areas AegisCore reads today; others (area_rules, cooldowns, ga_permissions) are added with their features.
var usedAreas = []string{"settings", "lists", "modules"}

// NewConfig returns an empty, not yet loaded config.
func NewConfig(client *Client) *Config {
	c := &Config{client: client, versions: map[string]int64{}}
	c.current.Store(&Snapshot{Settings: map[string]string{}, Lists: map[string][]string{}})
	return c
}

// Snapshot returns the current configuration (never nil). Safe for concurrent use.
func (c *Config) Snapshot() *Snapshot { return c.current.Load() }

// Refresh reloads the areas whose counter moved (all of them when force is set). On error the previous snapshot stays.
func (c *Config) Refresh(ctx context.Context, force bool) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	versions, err := c.readVersions(ctx)
	if err != nil {
		return nil, err
	}

	next := *c.Snapshot()
	var changed []string
	for _, area := range usedAreas {
		if loaded, ok := c.versions[area]; ok && !force && loaded == versions[area] {
			continue
		}
		if err := c.loadArea(ctx, area, &next); err != nil {
			return nil, err
		}
		changed = append(changed, area)
	}

	for _, area := range changed {
		c.versions[area] = versions[area]
	}
	c.current.Store(&next)
	return changed, nil
}

// Run polls until ctx ends. Database errors are logged once per outage; the last good snapshot stays in use.
func (c *Config) Run(ctx context.Context) {
	ticker := time.NewTicker(ConfigPollInterval)
	defer ticker.Stop()
	failing := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if _, err := c.Refresh(ctx, false); err != nil {
			if !failing && ctx.Err() == nil {
				log.WithError(err).Warn("VFILTER configuration unavailable; keeping the last good configuration")
			}
			failing = true
			continue
		}
		failing = false
	}
}

func (c *Config) readVersions(ctx context.Context) (map[string]int64, error) {
	rows, err := c.client.db.QueryContext(ctx, "SELECT area, version FROM filter_config_version")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := map[string]int64{}
	for rows.Next() {
		var area string
		var version int64
		if err := rows.Scan(&area, &version); err != nil {
			return nil, err
		}
		versions[area] = version
	}
	return versions, rows.Err()
}

func (c *Config) loadArea(ctx context.Context, area string, into *Snapshot) error {
	switch area {
	case "settings":
		settings := map[string]string{}
		err := c.query(ctx, "SELECT [key], value FROM filter_settings", func(rows *sql.Rows) error {
			var key, value string
			if err := rows.Scan(&key, &value); err != nil {
				return err
			}
			settings[key] = value
			return nil
		})
		into.Settings = settings
		return err
	case "lists":
		lists := map[string][]string{}
		err := c.query(ctx, "SELECT list, value FROM filter_lists ORDER BY id", func(rows *sql.Rows) error {
			var list, value string
			if err := rows.Scan(&list, &value); err != nil {
				return err
			}
			lists[list] = append(lists[list], value)
			return nil
		})
		into.Lists = lists
		return err
	case "modules":
		var modules []Module
		err := c.query(ctx, "SELECT name, type, module_ip, module_port, enabled FROM filter_modules ORDER BY id", func(rows *sql.Rows) error {
			var m Module
			if err := rows.Scan(&m.Name, &m.Type, &m.ModuleIP, &m.ModulePort, &m.Enabled); err != nil {
				return err
			}
			modules = append(modules, m)
			return nil
		})
		into.Modules = modules
		return err
	}
	return nil
}

func (c *Config) query(ctx context.Context, sqlText string, scan func(*sql.Rows) error) error {
	rows, err := c.client.db.QueryContext(ctx, sqlText)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}
