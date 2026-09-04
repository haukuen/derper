# Self-hosted Tailscale DERP Relay

English | [中文](README.md)

> **Scope**: this project targets the case of **sharing one relay among multiple independent tailnets** (e.g. friends who each run their own Tailscale network pooling relay bandwidth). If you only need a relay for a single tailnet, self-hosting a DERP is unnecessary — use the official [Peer Relay](https://tailscale.com/docs/features/peer-relay) feature instead.

This project provides two Docker images for running a Tailscale DERP relay service shared by **multiple independent tailnets**. Admission control is based on a node key allowlist managed by a standalone admission controller; derper consults it over the `--verify-client-url` protocol for every connecting client.


## Breaking changes

- **Removed the `TS_AUTHKEY` / `--verify-clients` mode**: it verified clients against a local tailscaled's netmap, which only works for a single tailnet and requires a TUN device and `NET_ADMIN`, incompatible with multi-tailnet sharing. Existing deployments should migrate to `VERIFY_CLIENT_URL`.
- **Admission now uses `--verify-client-url`**: derper submits each new connection's node key to the admission controller for verification. No tailscaled runs on relay servers anymore.

## Deployment

### 1. Get the config files

```bash
curl -O https://raw.githubusercontent.com/haukuen/derper/main/docker-compose.yaml
touch whitelist.txt   # must exist before the first start, or Docker mounts it as a directory
```

### 2. Populate the allowlist (pick one of the two methods)

**Method A: Tailscale API auto-sync (recommended)** — each member tailnet creates a read-only OAuth client and the controller periodically pulls its device list, maintaining that tailnet's node keys automatically: new devices join the allowlist on their own, devices removed from the tailnet are revoked automatically. Once configured, `whitelist.txt` usually no longer needs manual maintenance.

Generate an OAuth client (client ID + client secret) in the [Tailscale admin console](https://console.tailscale.com/admin/settings/trust-credentials) with the read-only **`devices:core:read`** scope, then add it to `docker-compose.yaml`:

```yaml
  admission-controller:
    environment:
      - TS_SYNC_CLIENTS=k1234567890abcdef:tskey-client-xxxx,k2345:tskey-client-yyyy
      - TS_SYNC_INTERVAL=5m   # optional, default 5 minutes
```

> **Credential safety**: only OAuth clients are accepted. **Never use a fully-permitted API access token** — such a key controls the entire tailnet (ACL edits, device removal, key issuance) and must not be given to a third-party server. Comma-separate multiple member tailnets, one client each.

**Method B: manual maintenance** — run the following on any node to obtain the node keys of **every device in that tailnet**:

Linux / macOS:

```bash
tailscale status --json | grep -o 'nodekey:[0-9a-zA-Z]*' | sort -u
```

Write the keys into `whitelist.txt`, one per line:

```
nodekey:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
nodekey:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
```

Edits take effect immediately on save, no restart needed. Both methods can coexist; the allowlist is their union.

### 3. Set the server address

Edit `docker-compose.yaml` and set `DERP_HOSTNAME` to the server's **public IP** (self-signed certificate by default) or **domain** (Let's Encrypt supported, see [Certificates](#certificates-optional)).

### 4. Start

```bash
docker compose up -d
```

This starts two containers: `admission-controller` (admission controller) and `derper` (relay).

### 5. Get the certificate fingerprint (public-IP setup)

With a bare public IP, derper generates a self-signed certificate whose fingerprint goes into the client-side DERP map:

```bash
docker logs derper
```

Key log line:

```
derper: Using self-signed certificate for IP address "YOUR_SERVER_PUBLIC_IP". Configure it in DERPMap using:
derper:   {"Name":"custom","RegionID":900,"HostName":"YOUR_SERVER_PUBLIC_IP","CertName":"sha256-raw:f829xxxxxxxxxxxxxx"}
```

Record the full `sha256-raw:...` value.

> **Upgrade note**: if an existing deployment used the `./derper-certs` bind mount, certificate storage has moved to a named volume. A fresh self-signed certificate is generated on first start and the `CertName` fingerprint changes — re-extract it from the logs and update every tailnet's DERP map.

## Client configuration

Each tailnet's administrator adds the same `derpMap` in the [Tailscale admin console](https://console.tailscale.com/admin/acls/file):

```json
{
  "derpMap": {
    "OmitDefaultRegions": false,
    "Regions": {
      "900": {
        "RegionID": 900,
        "RegionCode": "my-derp-1",
        "RegionName": "My DERP",
        "Nodes": [
          {
            "Name": "my-node-1",
            "RegionID": 900,
            "HostName": "YOUR_SERVER_PUBLIC_IP",
            "DERPPort": 40007,
            "STUNPort": 40008,
            "CertName": "sha256-raw:f829...",
            "IPv4": "YOUR_SERVER_PUBLIC_IP"
          }
        ]
      }
    }
  }
}
```

Field notes:

- `RegionID`: custom, use 900~999; assign a distinct Region ID per relay server.
- `DERPPort` / `STUNPort`: must match the server-side `DERP_PORT` / `STUN_PORT`.
- `CertName`: only needed for public-IP (self-signed) setups; omit with a domain + Let's Encrypt.
- `OmitDefaultRegions`: set `true` to disable official relays (optional). Clients pick the lowest-latency region among official and custom ones automatically after netcheck; yours is not forced.

## Verification

From any node whose tailnet has the DERP map applied:

```bash
tailscale netcheck       # the custom Region should report a concrete latency
```

Server side, the admission controller log (`docker logs admission-controller`) records every rejection: `DENY nodekey=... source=...`; the first admission of each key since startup is also logged as `ALLOW ... (first admission since start)` for auditing.

## Member management

**Add a member**:

1. Method A (sync): the member's tailnet admin generates a read-only OAuth client with `devices:core:read` and hands it to you; append it to `TS_SYNC_CLIENTS` and that tailnet's devices join the allowlist automatically.
2. Method B (manual): the member provides their node key (see [Populate the allowlist](#2-populate-the-allowlist-pick-one-of-the-two-methods)); append it to `whitelist.txt` and save.
3. The administrator of that member's tailnet adds all relay nodes to its DERP map.

**Remove a member**: Method A — drop the client from `TS_SYNC_CLIENTS` and recreate the controller (or simply remove the device from their tailnet); Method B — delete the key from `whitelist.txt` and save. Established connections cannot reconnect after they drop.

## Tailscale API auto-sync

The recommended setup from deployment (see [Populate the allowlist](#2-populate-the-allowlist-pick-one-of-the-two-methods), method A); behavior details:

- Synced lists live in memory only and are never written to `whitelist.txt`; manual and synced keys are merged (union). After a controller restart the first sync round takes a few seconds; until then the fail-closed policy applies and clients retry automatically, unnoticed.
- When one source's sync fails, its **previous list stays in force** and the error is recorded (visible via `GET /status`) — an API outage neither admits strangers nor wipes the list.
- If a member revokes their credential, syncing stops for that tailnet and the last synced list remains; switching to manual management is seamless.


## Environment variables

### derper

Required:

| Variable | Description |
|---|---|
| `DERP_HOSTNAME` | Public IP or domain |
| `VERIFY_CLIENT_URL` | Admission controller URL: `http://admission-controller:8081/verify` |

Optional:

| Variable | Default | Description |
|---|---|---|
| `VERIFY_CLIENT_FAIL_OPEN` | `false` | Whether to admit new clients when the controller is unreachable. Defaults to reject (fail-closed); upstream derper defaults to admit, which this image deliberately overrides — set `true` explicitly to relax |
| `WAIT_FOR_CONTROLLER` | empty | `host:port`; derper waits for the controller's health check to pass before starting (use within a compose network) |
| `DERP_PORT` | `40007` | DERP TLS listen port |
| `STUN_PORT` | `40008` | STUN UDP listen port |
| `DERP_CERTMODE` | `manual` | `manual` (self-signed / provided certs) or `letsencrypt` (domain required) |
| `ACME_EMAIL` | empty | ACME account email for letsencrypt mode |
| `DERP_EXTRA_ARGS` | empty | Extra args passed to derper, e.g. `--rate-config /etc/derper/rate-config.json` |

### admission-controller

Optional:

| Variable | Default | Description |
|---|---|---|
| `ADMIT_LISTEN` | `:8081` | HTTP listen address |
| `ADMIT_WHITELIST` | `/etc/derper/whitelist.txt` | Allowlist file path |
| `TS_SYNC_CLIENTS` | empty | OAuth clients for Tailscale auto-sync, `clientID:clientSecret`, comma-separated; requires the `devices:core:read` read-only scope |
| `TS_SYNC_INTERVAL` | `5m` | Sync polling interval |


## Certificates (optional)

With a domain and ports 80/443 reachable, switch to a Let's Encrypt certificate and drop `CertName` from the DERP map:

```yaml
  derper:
    environment:
      - DERP_HOSTNAME=derp.example.com
      - DERP_CERTMODE=letsencrypt
      - ACME_EMAIL=you@example.com
```

## Multi-node deployment

- Run one derper container per relay server, all pointing `VERIFY_CLIENT_URL` at the same controller.



## Features

- **Cross-tailnet traffic isolation**: DERP forwards only WireGuard ciphertext addressed to a destination node key; a party without the destination tailnet's session keys cannot decrypt anything — including allowlisted members among themselves.
- **No presence disclosure**: connection-state broadcasts (WatchConns) are served only to mesh-key-holding relay peers; clients cannot enumerate other members.
- **Unauthorized access rejected**: connections whose node key is not on the allowlist are rejected outright; when the controller is unreachable, the fail-closed policy rejects as well.
- **Bandwidth governance**: derper's experimental per-client rate limiting is available (`DERP_EXTRA_ARGS=--rate-config ...`) to cap any member's bandwidth usage.

## References

- [Official derper README](https://github.com/tailscale/tailscale/blob/main/cmd/derper/README.md)
- [Tailscale DERP servers doc](https://tailscale.com/kb/1232/derp-servers)
- [tailcfg.DERPAdmitClientRequest](https://pkg.go.dev/tailscale.com/tailcfg#DERPAdmitClientRequest)
