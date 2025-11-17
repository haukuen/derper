#!/bin/sh

set -e

if [ -z "$DERP_HOSTNAME" ]; then
    echo "Error: DERP_HOSTNAME environment variable must be set (your server's public IP)."
    exit 1
fi

DERP_CERT_DIR="/var/lib/derper"

mkdir -p "$DERP_CERT_DIR"

echo "Starting tailscaled in background..."
tailscaled --socket=/var/run/tailscale/tailscaled.sock --statedir=/var/lib/tailscale-derper-state &

echo "Waiting for tailscaled socket..."
while [ ! -S /var/run/tailscale/tailscaled.sock ]; do
  sleep 1
done
echo "tailscaled socket is ready."

echo "Starting DERP server, hostname: $DERP_HOSTNAME..."
exec derper \
    --hostname="$DERP_HOSTNAME" \
    -certmode manual \
    -certdir "$DERP_CERT_DIR" \
    -http-port -1 \
    -a ":$DERP_PORT" \
    -stun-port "$STUN_PORT" \
    --verify-clients