package vfilter

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestClient creates a throwaway database from testdata/contract-schema.sql on the SQL Server in
// VFILTER_TEST_DSN (server-level DSN, no database). Skipped when unset.
func newTestClient(t *testing.T, module string) *Client {
	t.Helper()
	server := os.Getenv("VFILTER_TEST_DSN")
	if server == "" {
		t.Skip("set VFILTER_TEST_DSN (e.g. sqlserver://sa:pw@127.0.0.1:14333) to run SQL Server tests")
	}
	ctx := context.Background()
	master, err := sql.Open("sqlserver", server)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("vfilter_go_%d", time.Now().UnixNano())
	if _, err := master.ExecContext(ctx, "CREATE DATABASE ["+name+"]"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = master.Exec("ALTER DATABASE [" + name + "] SET SINGLE_USER WITH ROLLBACK IMMEDIATE; DROP DATABASE [" + name + "]")
		_ = master.Close()
	})

	u, _ := url.Parse(server)
	q := u.Query()
	q.Set("database", name)
	u.RawQuery = q.Encode()
	client, err := OpenDSN(ctx, u.String(), module)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	schema, err := os.ReadFile("testdata/contract-schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.db.ExecContext(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	return client
}

func exec(t *testing.T, c *Client, query string, args ...any) {
	t.Helper()
	if _, err := c.db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func TestConfigReloadsOnlyChangedAreas(t *testing.T) {
	c := newTestClient(t, "Gateway")
	exec(t, c, `INSERT INTO filter_config_version(area, version) VALUES ('settings',1),('lists',1),('modules',1);
		INSERT INTO filter_settings([key], value, value_type, [group]) VALUES ('gateway.gm_refusal_code','9','int','gateway');
		INSERT INTO filter_lists(list, value) VALUES ('allowed_ips','127.0.0.0/8');
		INSERT INTO filter_modules(type,name,module_ip,module_port,guard_ip,guard_port,enabled) VALUES ('agent','Agent1','127.0.0.1',15882,'127.0.0.1',15882,1)`)
	cfg := NewConfig(c)

	changed, err := cfg.Refresh(context.Background(), false)
	if err != nil || len(changed) != 3 {
		t.Fatalf("first refresh: %v %v", changed, err)
	}
	snap := cfg.Snapshot()
	if snap.Int("gateway.gm_refusal_code", 6) != 9 || snap.Lists["allowed_ips"][0] != "127.0.0.0/8" || snap.Modules[0].Target() != "agent1" {
		t.Fatalf("unexpected snapshot %+v", snap)
	}

	exec(t, c, `INSERT INTO filter_lists(list, value) VALUES ('blocked_ips','9.9.9.9'); UPDATE filter_config_version SET version = 2 WHERE area = 'lists'`)
	changed, err = cfg.Refresh(context.Background(), false)
	if err != nil || strings.Join(changed, ",") != "lists" || len(cfg.Snapshot().Lists["blocked_ips"]) != 1 {
		t.Fatalf("second refresh: %v %v", changed, err)
	}
}

func TestCommandsRunOnceExpireAndSkipOthers(t *testing.T) {
	c := newTestClient(t, "Agent1")
	exec(t, c, `INSERT INTO filter_commands(command, target, payload, created_by, created_at) VALUES
		('kick','Knight','{"reason":"afk"}',1,SYSUTCDATETIME()),
		('kick','Old',NULL,1,DATEADD(minute,-20,SYSUTCDATETIME())),
		('restart','agent1',NULL,1,SYSUTCDATETIME())`)
	exec(t, c, `INSERT INTO filter_commands(command, target, status, created_by, created_at) VALUES ('kick','Gone','cancelled',1,SYSUTCDATETIME())`)

	var mu sync.Mutex
	var ran []string
	ctx, cancel := context.WithCancel(context.Background())
	go c.RunCommands(ctx, []string{"kick"}, func(_ context.Context, cmd Command) Outcome {
		mu.Lock()
		ran = append(ran, cmd.Target+":"+cmd.PayloadString("reason"))
		mu.Unlock()
		return Done("kicked")
	})
	time.Sleep(2500 * time.Millisecond)
	cancel()

	if strings.Join(ran, ",") != "Knight:afk" {
		t.Fatalf("ran %v", ran)
	}
	rows, _ := c.db.Query("SELECT target, status, ISNULL(result,'') FROM filter_commands ORDER BY id")
	defer rows.Close()
	got := []string{}
	for rows.Next() {
		var target, status, result string
		_ = rows.Scan(&target, &status, &result)
		got = append(got, target+"="+status+"/"+result)
	}
	want := "Knight=done/kicked,Old=failed/expired,agent1=pending/,Gone=cancelled/"
	if strings.Join(got, ",") != want {
		t.Fatalf("got %v want %s", got, want)
	}
}

func TestClaimAndCancelNeverBothWin(t *testing.T) {
	c := newTestClient(t, "Agent1")
	for i := 0; i < 20; i++ {
		var id int64
		if err := c.db.QueryRow("INSERT INTO filter_commands(command,target,created_by,created_at) OUTPUT inserted.id VALUES ('kick','x',1,SYSUTCDATETIME())").Scan(&id); err != nil {
			t.Fatal(err)
		}
		var cancelled int64
		var claimed *Command
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			res, err := c.db.Exec("UPDATE filter_commands SET status='cancelled' WHERE id=@p1 AND status='pending'", id)
			if err == nil {
				cancelled, _ = res.RowsAffected()
			}
		}()
		go func() { defer wg.Done(); claimed, _ = c.claim(context.Background(), id) }()
		wg.Wait()
		if (cancelled == 1) == (claimed != nil) {
			t.Fatalf("command %d: cancelled=%d claimed=%v", id, cancelled, claimed != nil)
		}
	}
}

func TestStatusSessionsAndLogs(t *testing.T) {
	c := newTestClient(t, "Agent1")
	ctx := context.Background()
	if err := c.WriteStatus(ctx, Status{Module: "Agent1", State: "running", OnlineCount: 3, Connections: 4, Version: "aegis", Uptime: 90 * time.Second}); err != nil {
		t.Fatal(err)
	}
	var state string
	var up int
	_ = c.db.QueryRow("SELECT state, DATEDIFF(second, started_at, heartbeat_at) FROM filter_status WHERE module='Agent1'").Scan(&state, &up)
	if state != "running" || up < 89 || up > 91 {
		t.Fatalf("status %s %d", state, up)
	}

	mirror := NewSessionMirror(c)
	runCtx, cancel := context.WithCancel(ctx)
	go mirror.Run(runCtx)
	mirror.Opened(SessionInfo{Key: "s1", IP: "10.0.0.5", Username: "sara", Charname: "فارس"})
	time.Sleep(300 * time.Millisecond)
	var name string
	_ = c.db.QueryRow("SELECT charname FROM filter_sessions").Scan(&name)
	if name != "فارس" {
		t.Fatalf("charname %q", name)
	}
	mirror.Closed("s1")
	time.Sleep(300 * time.Millisecond)
	var count int
	_ = c.db.QueryRow("SELECT COUNT(*) FROM filter_sessions").Scan(&count)
	if count != 0 {
		t.Fatalf("sessions left %d", count)
	}

	events := NewEventLog(c)
	go events.Run(runCtx)
	events.Write(Event{Level: "security", Category: "login", Message: "login refused: gateway in gm mode", IP: "1.2.3.4"})
	time.Sleep(1500 * time.Millisecond)
	cancel()
	_ = c.db.QueryRow("SELECT COUNT(*) FROM filter_logs WHERE category='login' AND module='Agent1'").Scan(&count)
	if count != 1 {
		t.Fatalf("logs %d", count)
	}
}
