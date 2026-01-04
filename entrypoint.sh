#!/bin/sh

set -e

if [ -z "$DERP_HOSTNAME" ]; then
    echo "Error: DERP_HOSTNAME environment variable must be set (your server's public IP)."
    exit 1
fi

DERP_CERT_DIR="/var/lib/derper"
TAILSCALED_SOCKET="/var/run/tailscale/tailscaled.sock"
SOCKET_TIMEOUT="${TAILSCALED_TIMEOUT:-30}"

mkdir -p "$DERP_CERT_DIR"

echo "Starting tailscaled in background..."
tailscaled --socket="$TAILSCALED_SOCKET" --statedir=/var/lib/tailscale-derper-state &
TAILSCALED_PID=$!

echo "Waiting for tailscaled socket (timeout: ${SOCKET_TIMEOUT}s)..."
elapsed=0
while [ ! -S "$TAILSCALED_SOCKET" ]; do
  if [ $elapsed -ge $SOCKET_TIMEOUT ]; then
    echo "Error: tailscaled socket not ready after ${SOCKET_TIMEOUT}s"
    exit 1
  fi
  
  if ! kill -0 $TAILSCALED_PID 2>/dev/null; then
    echo "Error: tailscaled process died unexpectedly"
    exit 1
  fi
  
  sleep 1
  elapsed=$((elapsed + 1))
done
echo "tailscaled socket is ready."

if [ -n "$TS_AUTHKEY" ]; then
    echo "Authenticating tailscaled..."
    if ! tailscale up --authkey="$TS_AUTHKEY" --hostname="$DERP_HOSTNAME-derp"; then
        echo "Error: Failed to authenticate with Tailscale"
        exit 1
    fi
    echo "Successfully authenticated with Tailscale."
fi

echo "Starting DERP server, hostname: $DERP_HOSTNAME..."
exec derper \
    --hostname="$DERP_HOSTNAME" \
    -certmode manual \
    -certdir "$DERP_CERT_DIR" \
    -http-port -1 \
    -a ":$DERP_PORT" \
    -stun-port "$STUN_PORT" \
    --verify-clients