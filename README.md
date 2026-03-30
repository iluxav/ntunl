# etunl

A reverse proxy and network tunnel that exposes local services through a public server. Route HTTP services by subdomain and TCP services (databases, etc.) through a single tunnel connection.

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
```

- **HTTP**: routed by subdomain via `Host` header (`app.etunl.com` → `localhost:3030`)
- **TCP**: routed via TLS/SNI on a shared port (`db1.etunl.com:15432` → `localhost:5432`)
- **Local access**: `/etc/hosts` + local proxy on the same machine — no tunnel needed

## Quick Start

Generate a shared token and config files:

```bash
# On the server (DO machine)
etunl init --mode server

# On the client (local machine)
etunl init --mode client --server tunnel.yourdomain.com
```

Both commands generate a random 256-bit token. Copy the token from one to the other so they match.

Then add routes and start:

```bash
etunl add --name app --type http --target localhost:3030
etunl client
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
etunl init --mode client --server tunnel.yourdomain.com
etunl client
```

This creates `~/.etunl/config.yaml`. Make sure the token matches the server. You can also create it manually:

```yaml
server: tunnel.yourdomain.com
token: "your-secret-token"

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
```

### 3. DNS (Cloudflare)

Add a wildcard DNS record pointing to your DO server:

```
*.yourdomain.com  →  A  →  <DO server IP>
```

Enable Cloudflare proxy for free TLS.

### 4. Local access (same machine)

Add to `/etc/hosts`:

```
127.0.0.1  app.local.env
127.0.0.1  server.local.env
```

HTTP routes are available via subdomain on port 80. TCP routes are available on their configured `local_port`.

## Usage

### Manage routes

```bash
# Add a route
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

## Commands

| Command | Description |
|---|---|
| `etunl init` | Generate config with a secret token |
| `etunl server` | Run the tunnel server on a public machine |
| `etunl client` | Run the tunnel client on your local machine |
| `etunl connect` | Tunnel a local port to a remote TCP service |
| `etunl add` | Add a route to the config |
| `etunl remove` | Remove a route from the config |
| `etunl list` | List configured routes |

Use `etunl <command> --help` for details on each command.
