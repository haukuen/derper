# Tailscale DERP Relay Server Docker Image

[中文](README.md) | English

This project provides a Docker image for quickly deploying a client-authenticated Tailscale DERP relay server, requiring only a public IP address.

## Quick Start

### 1. Download Configuration File

```bash
curl -O https://raw.githubusercontent.com/haukuen/derper/main/docker-compose.yaml
```

### 2. Get Tailscale Auth Key

For the DERP server to verify connecting clients, it must be logged into your Tailscale network.

1. Log in to [Tailscale Admin Console - Keys page](https://login.tailscale.com/admin/settings/keys).
2. Click "Generate auth key".
3. Recommended settings:
    - **Reusable**: Check (prevents key invalidation after container rebuild)
    - **Ephemeral**: Uncheck
    - **Tags**: Optional (e.g., `tag:derper` for easier ACL management).
4. Copy the generated Auth Key starting with `tskey-auth-xxxxxx`.


### 3. Modify Configuration

Edit the `docker-compose.yaml` file and fill in your **public IP** and the **Auth Key** obtained in the previous step.


### 4. Start Service

```bash
docker-compose up -d
```

### 5. Get CertName

View container logs to obtain the certificate name:

```bash
docker logs derper
```

Find the `CertName` information at the end of the logs in a format similar to:

```
derper: Using self-signed certificate for IP address "YOUR_SERVER_PUBLIC_IP". Configure it in DERPMap using:
derper:   {"Name":"custom","RegionID":900,"HostName":"YOUR_SERVER_PUBLIC_IP","CertName":"sha256-raw:f829xxxxxxxxxxxxxx"}
```

Copy the `sha256-raw:f829...` part for later configuration.

## Configure Tailscale ACL

### 1. Access Admin Console

Log in to [Tailscale Admin Console](https://login.tailscale.com/admin/acls).

### 2. Edit ACL Configuration

Add the `derpMap` section to your ACL JSON configuration. Here's a complete example:

**Configuration Details:**
- `OmitDefaultRegions`: Set to `true` to disable all official Tailscale relays (optional)
- `RegionID`: Custom region ID, officially recommended between 900 and 999
- `RegionCode`: Custom name that will appear in netcheck
- `HostName`: Your relay server's public IP address
- `CertName`: Paste the certificate name obtained in the previous step

```json
{
  "derpMap": {
    "OmitDefaultRegions": true,
    "Regions": {
      "900": {
        "RegionID": 900,
        "RegionCode": "my-derp-1",
        "RegionName": "My First DERP",
        "Nodes": [
          {
            "Name": "my-node-1",
            "RegionID": 900,
            "HostName": "YOUR_SERVER_PUBLIC_IP",
            "DERPPort": 40007,
            "STUNPort": 40008,
            "CertName": "sha256-raw:f829..."
          }
        ]
      }
    }
  }
}
```
For multiple relay locations, add "901": { ... } and so on

### 3. Save Configuration

Save the ACL configuration to apply the changes.

## Verify Deployment

Run the following command on any Tailscale device to verify the relay is working:

```bash
tailscale netcheck
```

Expected results:

1. DERP latency output should include your custom Region.
2. The node's latency should display a specific value, not blank or error.

## Notes

- Ensure ports 40007/tcp and 40008/udp are open on your server.

## Reference

[Tailscale Configuration Documentation](https://pkg.go.dev/tailscale.com/tailcfg#DERPRegion)
