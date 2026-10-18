// Command vfilter-supervisor carries out the VFilter control panel's Start / Stop / Restart for AegisCore's
// containers (a stopped server cannot start itself) and reports modules that are down. See
// _docs/vfilter-integration.md. It needs VFILTER_DSN, VFILTER_MODULE_CONTAINERS and the Docker socket.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Harvi8/AegisCore/framework/vfilter"
	log "github.com/sirupsen/logrus"
)

const defaultDockerSocket = "/var/run/docker.sock"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	modules, err := parseModules(os.Getenv("VFILTER_MODULE_CONTAINERS"))
	if err != nil {
		log.Fatal(err)
	}
	dsn := os.Getenv("VFILTER_DSN")
	if dsn == "" {
		log.Fatal("VFILTER_DSN is required")
	}
	socket := os.Getenv("DOCKER_SOCKET")
	if socket == "" {
		socket = defaultDockerSocket
	}

	client, err := connect(ctx, dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	s := &supervisor{modules: modules, docker: newDockerClient(socket)}
	log.WithField("modules", len(modules)).Info("VFILTER supervisor running")

	go client.RunCommands(ctx, []string{"start", "stop", "restart", "reload"}, s.handle)

	ticker := time.NewTicker(vfilter.HeartbeatInterval)
	defer ticker.Stop()
	for {
		for _, status := range s.downStatuses(ctx) {
			if err := client.WriteStatus(ctx, status); err != nil && ctx.Err() == nil {
				log.WithError(err).Warn("supervisor could not write module status")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// connect retries until the database answers, so the supervisor can start before SQL Server.
func connect(ctx context.Context, dsn string) (*vfilter.Client, error) {
	for delay := time.Second; ; delay = min(delay*2, 30*time.Second) {
		client, err := vfilter.OpenDSN(ctx, dsn, "supervisor")
		if err == nil {
			return client, nil
		}
		log.WithError(err).Warnf("VFILTER database not reachable; retrying in %s", delay)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
}
