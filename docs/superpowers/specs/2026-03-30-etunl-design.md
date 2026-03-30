# etunl — Reverse Proxy & Network Tunnel

## Overview

A single Go binary (`etunl`) that creates a secure tunnel between a public DigitalOcean server and a local homelab machine, enabling subdomain-based routing to local services from anywhere.

## Goals

- Expose local HTTP services via public subdomains (e.g. `app.etunl.com` → `localhost:3030`)
- Expose local TCP services (databases) via TLS/SNI on a shared port
- Dynamic config — add/remove routes without restart
- Local access via `/etc/hosts` on the same machine
- Remote access via `etunl connect` from any machine
- Single binary, multiple modes via subcommands

## Non-Goals

- Multi-tenancy, user accounts, billing
- High availability or clustering
- Automatic DNS/hosts management

## Architecture

```
                    Internet
                       |
              ┌────────┴────────┐
              │   Cloudflare    │  Wildcard DNS: *.etunl.com → DO IP
              │   (TLS term)   │  Proxied mode, free TLS
              └────────┬────────┘
                       |
              ┌────────┴────────┐
              │   DO Server     │  etunl server
              │   - HTTP :80    │  Subdomain routing via Host header
              │   - TCP :15432  │  TLS/SNI-based DB routing
              │   - WS /tunnel  │  Tunnel endpoint
              └────────┬────────┘
                       |
              Single WebSocket tunnel (outbound from local)
                       |
              ┌────────┴────────┐
              │  Local Machine  │  etunl client
              │  - Tunnel conn  │  Connects to DO, routes to backends
              │  - Local HTTP   │  :80 for local subdomain access
              │  - Local TCP    │  Per-service ports for local DB access
              └────────┬────────┘
                       |
              ┌────┬───┴───┬────┐
              :3000  :3030  :5432  :5433
              server  app   db1    db2
```

## Components

### Single Binary, Multiple Modes

```
etunl server              — runs on DO, accepts public traffic + tunnel
etunl client              — runs locally, connects to DO, routes to backends
etunl connect --name db1  — runs on any machine, tunnels to a TCP service
etunl add/remove/list     — CLI config management helpers
```

### Tunnel Protocol

Single WebSocket connection carrying multiplexed streams.

**Frame format:**

```
┌─────────┬────────────┬─────────┬─────────────┐
│ Type(1B)│ StreamID(4B)│ Len(4B) │ Payload     │
└─────────┴────────────┴─────────┴─────────────┘
```

**Frame types:**

| Type | Name        | Direction        | Purpose                                  |
|------|-------------|------------------|------------------------------------------|
| 0x01 | StreamOpen  | Server → Client  | New connection for a named route         |
| 0x02 | StreamData  | Bidirectional    | Raw bytes for a stream                   |
| 0x03 | StreamClose | Either           | Close a stream (with optional error)     |
| 0x04 | RouteSync   | Client → Server  | Register/update available routes         |
| 0x05 | Ping/Pong   | Either           | Keepalive, detect dead connections       |

**Connection lifecycle:**

1. Client connects to `wss://tunnel.etunl.com/tunnel` with `Authorization: Bearer <token>`
2. Server validates token, upgrades to WebSocket
3. Client sends `RouteSync` with its route list
4. Incoming connections produce `StreamOpen` → `StreamData` exchanges → `StreamClose`

### HTTP Routing

**Public (DO server):**

1. Request arrives at `app.etunl.com:80`
2. Server extracts subdomain from `Host` header
3. Looks up route in table (populated by client's `RouteSync`)
4. Opens `StreamOpen(route="app")`, forwards HTTP request bytes
5. Client dials `localhost:3030`, pipes request/response
6. Server writes response back to public client

**Local (same machine as client):**

1. `/etc/hosts`: `127.0.0.1 app.local.env server.local.env`
2. Local HTTP proxy on `:80` extracts subdomain from `Host` header
3. Routes directly to target — no tunnel involved

### TCP Routing

**Public (DO server) — TLS/SNI on shared port:**

1. TCP listener on `:15432`
2. Client initiates TLS → server reads SNI hostname (e.g. `db1.etunl.com`)
3. Extracts subdomain `db1`, looks up route
4. Opens `StreamOpen(route="db1")`, pipes raw bytes through tunnel
5. Client dials `localhost:5432`

**Remote access via `etunl connect`:**

1. User runs `etunl connect --name db1 --local-port 5432`
2. Opens local listener on `:5432`
3. Connects to DO server via WebSocket, requests route `db1`
4. DBeaver connects to `localhost:5432` → tunneled through DO → client → `localhost:5432`

**Local (same machine as client):**

- One local port per TCP service (no TLS/SNI needed)
- e.g. `:15432` → `localhost:5432`, `:15433` → `localhost:5433`

## Configuration

**File:** `~/.etunl/config.yaml` (client side)

```yaml
server: tunnel.etunl.com
token: "my-secret-token"

routes:
  - name: app
    type: http
    target: localhost:3030
  - name: server
    type: http
    target: localhost:3000
  - name: db1
    type: tcp
    target: localhost:5432
    local_port: 15432
  - name: db2
    type: tcp
    target: localhost:5433
    local_port: 15433
```

**Server config:** `~/.etunl/server.yaml`

```yaml
listen_http: :80
listen_tcp: :15432
token: "my-secret-token"
```

**Hot-reload:** `fsnotify` watches config file. On change:
- Parse new config
- If valid: apply changes, send `RouteSync` to server
- If invalid: log error, keep previous config

**CLI helpers:**

```bash
etunl add --name db3 --type tcp --target localhost:5434 --local-port 15434
etunl remove --name db3
etunl list
```

These edit the YAML file, triggering hot-reload.

## Security & Auth

- Pre-shared token in `Authorization: Bearer <token>` header on WebSocket handshake
- Server rejects connections with invalid token before upgrading
- `etunl connect` also requires the token
- Token stored in config files
- No user accounts, sessions, or OAuth

## Error Handling & Resilience

**Tunnel disconnect:**
- Client reconnects with exponential backoff: 1s, 2s, 4s... max 30s
- Retries indefinitely
- On reconnect: sends fresh `RouteSync` to re-register routes
- In-flight streams are dropped (acceptable for homelab)

**Target unreachable (e.g. localhost:3030 is down):**
- Client fails to dial → sends `StreamClose` with error
- HTTP: server returns `502 Bad Gateway`
- TCP: connection closed

**Invalid route (unconfigured subdomain):**
- HTTP: `404 Not Found`
- TCP/SNI: connection closed

**Config errors:**
- Malformed YAML → logged, previous config kept
- Duplicate route names → rejected, logged

## Project Structure

```
etunl/
├── cmd/
│   └── etunl/
│       └── main.go              # CLI entrypoint (cobra)
├── internal/
│   ├── server/
│   │   ├── server.go            # DO tunnel server
│   │   ├── http_proxy.go        # HTTP subdomain routing
│   │   └── tcp_proxy.go         # TLS/SNI TCP routing
│   ├── client/
│   │   ├── client.go            # Tunnel client, WebSocket connection
│   │   ├── router.go            # Route lookup, dial local targets
│   │   └── local_proxy.go       # Local HTTP + TCP listeners
│   ├── tunnel/
│   │   ├── frame.go             # Frame encoding/decoding
│   │   ├── stream.go            # Stream multiplexer
│   │   └── conn.go              # WebSocket wrapper
│   ├── config/
│   │   ├── config.go            # Config struct, load/save YAML
│   │   └── watcher.go           # File watcher for hot-reload
│   └── connect/
│       └── connect.go           # etunl connect (remote TCP access)
├── config.example.yaml
├── go.mod
└── go.sum
```

**Dependencies:**
- `gorilla/websocket` — WebSocket implementation
- `cobra` — CLI framework
- `fsnotify` — config file watching
- `yaml.v3` — config parsing

## Data Flow Summary

All traffic — HTTP and TCP — flows through the same tunnel. The only difference is how connections enter the DO server (HTTP `Host` header vs TLS SNI vs `etunl connect`). Once inside the tunnel, everything is a named stream of bytes routed to the appropriate local target.
