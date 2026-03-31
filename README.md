# etunl

A reverse proxy and network tunnel that exposes local services through a public server. Route HTTP services by subdomain and TCP services (databases, etc.) through a single tunnel connection. Includes a built-in web dashboard for managing routes.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/iluxav/ntunl/main/install.sh | sh
```

Or pin a specific version:

```bash
VERSION=v0.1.0 curl -fsSL https://raw.githubusercontent.com/iluxav/ntunl/main/install.sh | sh
```

### Build from source

```bash
git clone https://github.com/iluxav/ntunl.git
cd ntunl
go build -o etunl ./cmd/etunl
```

## Architecture

```
Internet → Cloudflare (TLS) → DO Server (etunl server)
                                    │
                              WebSocket tunnel
                                    │
                              Local Machine (etunl client)
                                    │
                         ┌──────────┼──────────┐
                       :3000      :3030      :5432
                       server      app        db
                                    │
                              :8080 dashboard
```

- **HTTP**: routed by subdomain via `Host` header (`app.etunl.com` → `localhost:3030`)
- **TCP**: routed via TLS/SNI on a shared port (`db1.etunl.com:15432` → `localhost:5432`)
- **Dashboard**: built-in web UI for managing routes (`admin.etunl.com` → `localhost:8080`)
- **Local access**: `/etc/hosts` + local proxy on the same machine — no tunnel needed

## Quick Start

```bash
# 1. On the server (DO machine)
etunl init --mode server
etunl server

# 2. On the client (local machine) — use the token from the server
etunl init --mode client --server etunl.com <token>
etunl client
```

The client `init` command pre-configures an `admin` route pointing to the built-in dashboard. Once connected, open `https://admin.etunl.com` to manage routes from your browser.

To add routes via CLI instead:

```bash
etunl add --name app --type http --target localhost:3030
```

## Setup

### 1. Server (public machine, e.g. DigitalOcean)

```bash
etunl init --mode server
etunl server
```

This creates `~/.etunl/server.yaml` with a generated token. You can also create it manually:

```yaml
listen_http: ":80"
listen_tcp: ":15432"
token: "your-secret-token"
```

### 2. Client (local machine)

```bash
etunl init --mode client --server etunl.com <token>
etunl client
```

This creates `~/.etunl/config.yaml` with the provided token and an `admin` route for the dashboard. You can also create it manually:

```yaml
server: etunl.com
token: "your-secret-token"

routes:
  - name: admin
    type: http
    target: localhost:8080

  - name: app
    type: http
    target: localhost:3030

  - name: db1
    type: tcp
    target: localhost:5432
    local_port: 15432
```

### 3. DNS (Cloudflare)

Add DNS records pointing to your server:

```
yourdomain.com    →  A  →  <server IP>   (Proxied)
*.yourdomain.com  →  A  →  <server IP>   (Proxied)
```

Set SSL/TLS mode to **Flexible** (Cloudflare handles TLS, server listens on HTTP).

### 4. Local access (same machine as client)

Add to `/etc/hosts`:

```
127.0.0.1  app.local.env
127.0.0.1  server.local.env
```

HTTP routes are available via subdomain on port 80. TCP routes are available on their configured `local_port`.

## Dashboard

The client includes a built-in web dashboard for managing routes. It starts automatically on port 8080.

- **Locally**: `http://localhost:8080`
- **Remotely**: `https://admin.yourdomain.com` (pre-configured by `etunl init`)

From the dashboard you can:
- View tunnel connection status
- See all configured routes
- Add new routes
- Remove existing routes

Changes are saved to the config file and hot-reloaded — no restart needed.

```bash
# Custom dashboard port
etunl client --dashboard :9090

# Disable dashboard
etunl client --dashboard ""
```

## Usage

### Manage routes (CLI)

```bash
# Add an HTTP route
etunl add --name api --type http --target localhost:8080

# Add a TCP route
etunl add --name redis --type tcp --target localhost:6379 --local-port 16379

# List routes
etunl list

# Remove a route
etunl remove --name api
```

Routes are hot-reloaded — no restart needed.

### Remote TCP access

From any machine, tunnel a local port to a TCP service through the server:

```bash
etunl connect --name db1 --local-port 5432
```

Then connect your client (DBeaver, psql, etc.) to `localhost:5432`.

### Health check

The server exposes a health endpoint:

```bash
curl https://yourdomain.com/health
```

Returns tunnel status, connected routes, and route details.

## Commands

| Command | Description |
|---|---|
| `etunl init [token]` | Initialize config with a token (generates one if not provided) |
| `etunl server` | Run the tunnel server on a public machine |
| `etunl client` | Run the tunnel client with dashboard on local machine |
| `etunl connect` | Tunnel a local port to a remote TCP service |
| `etunl add` | Add a route to the config |
| `etunl remove` | Remove a route from the config |
| `etunl list` | List configured routes |

Use `etunl <command> --help` for details on each command.
