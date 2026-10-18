# VFILTER integration (website control panel → AegisCore)

**Status:** in progress (branch `feat/vfilter-contract`) · **Date:** 2026-10-18

## Why
The VFilter control panel (repo `ENGMElaraby/VFilterWebsite`) configures and controls a Silkroad server through one
SQL Server database, `VFILTER`, defined by the shared contract (`VFilter/docs/shared-contract.md`, 0.3.3). VFilter was
first a proxy in front of the closed vSRO binaries. With AegisCore as the production server we own the source, so the
filter's job moves **inside** AegisCore. The website does not change: AegisCore implements the "filter" side of the contract.

## Decisions (owner, 2026-10-18)
- AegisCore is the production server. It connects to SQL Server for `VFILTER` only. Game data stays in MySQL.
- The website's Start/Stop/Restart buttons mean **full process control**:
  - Stop: the module's process stops.
  - Start: it starts again.
  - Restart: it stops and starts.
  An always-on **supervisor** does this through the Docker Engine API, because a stopped process cannot start itself.
- Integration is optional: with `VFILTER_DSN` empty everything stays off and AegisCore behaves exactly as before.

## Components
| Component | Where | Contract duties |
| --- | --- | --- |
| `framework/vfilter` | shared Go package | SQL Server client (`github.com/microsoft/go-mssqldb`). Config snapshots: settings, lists, modules, polled every 2 s per area. Command claim and finish: pending → running → done/failed, 10-minute expiry. Status upsert using the DB clock. Session mirror. Batched log writer. |
| `cmd/vfilter-supervisor` | new container (`vfilter-supervisor` service, `vfilter` profile) | Claims `start` / `stop` / `restart` for modules and `all`, and `reload`. Maps each module to its compose container and calls the Docker API over `/var/run/docker.sock`. Writes the status row for modules whose container is **not** running (state `stopped`/`error`), so the dashboard shows them fresh, not stale. |
| gateway-server | module `Gateway` | Heartbeat: connections, gateway mode, version, uptime. Claims `set_gateway_mode`. Login gate before the password check: in GM/Inspection only `allowed_ips` (CIDR) may log in, refused with the `gateway.gm_refusal_code` / `gateway.inspection_refusal_code` settings (default 6). `blocked_ips` (CIDR) are refused with code 2. Refusals are logged. |
| agent-server | module `Agent1` (`VFILTER_MODULE_NAME`) | Heartbeat: connections, in-world players. Mirrors in-world sessions (character join → insert, session closed → delete). Claims `kick` (character / username / IP), `ban`, and `notice` (`BroadcastNotice`). |

### Command ownership
Every command is claimed by exactly one process. Each process only queries the commands it owns:
- supervisor: `start`, `stop`, `restart`, `reload`
- gateway: `set_gateway_mode`
- agent: `kick`, `ban`, `notice`

### Ban
`ban` sets `SRO_ACCOUNT.USER.STATUS = 0` (the existing GM ban, enforced at Gateway login) and closes the session.
AegisCore has no ban expiry yet, so a **timed** ban fails with `timed bans not supported yet`. It is never silently made
permanent. A permanent ban (`duration_minutes: null`) is applied.

### Docker restart policy
`gateway-server` and `agent-server` change from `restart: always` to `restart: unless-stopped`. A container stopped from the
panel then stays stopped, even across a Docker daemon restart, until the panel starts it again. Crash restarts are unchanged.

### Security
The supervisor mounts the Docker socket, which is root-equivalent on the host. It talks to nothing but SQL Server and Docker.
It only acts on containers listed in its module map, and only for claimed contract commands. Credentials come from env
(`VFILTER_DSN`) only.

## Configuration
| Env | Used by | Meaning |
| --- | --- | --- |
| `VFILTER_DSN` | all | e.g. `sqlserver://vfilter_svc:…@sqlhost:1433?database=VFILTER`. Empty = integration off |
| `VFILTER_MODULE_NAME` | gateway, agent | Status row name. Defaults to `Gateway` / `Agent1` |
| `VFILTER_MODULE_CONTAINERS` | supervisor | `Gateway=aegiscore-gateway-server-1,Agent1=aegiscore-agent-server-1` |

## Phases
1. **This branch:**
   - the framework package
   - the supervisor
   - heartbeat, commands, sessions and logs in gateway and agent
   - the GM/Inspection and blocked-IP login gate

   With this, the panel's dashboard, Start/Stop/Restart, gateway mode, Sessions/Kick/Ban and Logs all work against AegisCore.
2. Later:
   - HWID lists (needs a client HWID source)
   - IP/PC/cafe limits
   - Game Assistant permissions in the GM command handler
   - area rules and cooldowns as native gameplay checks
