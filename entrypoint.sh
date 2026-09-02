#!/bin/sh
set -e

DERP_CERT_DIR="/var/lib/derper"
mkdir -p "$DERP_CERT_DIR"

if [ -z "$DERP_HOSTNAME" ]; then
    echo "Error: DERP_HOSTNAME environment variable must be set (public IP or domain)."
    exit 1
fi

ARGS=""
if [ -n "$DERP_EXTRA_ARGS" ]; then
    ARGS="$ARGS $DERP_EXTRA_ARGS"
fi

if [ -n "$DERP_PORT" ]; then
    ARGS="$ARGS -a :$DERP_PORT"
fi
if [ -n "$STUN_PORT" ]; then
    ARGS="$ARGS -stun-port $STUN_PORT"
fi
if [ "${DERP_STUN:-true}" != "true" ]; then
    ARGS="$ARGS -stun=false"
fi

# Certificate mode: manual (self-signed for IP / provided files, default)
# or letsencrypt (requires a domain and ports 80/443 reachable).
CERTMODE="${DERP_CERTMODE:-manual}"
if [ "$CERTMODE" = "letsencrypt" ]; then
    ARGS="$ARGS -certmode letsencrypt"
    if [ -n "$ACME_EMAIL" ]; then
        ARGS="$ARGS -acme-email $ACME_EMAIL"
    fi
    # Let's Encrypt HTTP-01 needs port 80 for challenges/renewal.
    ARGS="$ARGS -http-port ${DERP_HTTP_PORT:-80}"
else
    ARGS="$ARGS -certmode manual -certdir $DERP_CERT_DIR -http-port ${DERP_HTTP_PORT:--1}"
fi

# --- admission control ---
if [ -n "$VERIFY_CLIENT_URL" ]; then
    # fail-open defaults to true upstream, which silently admits everyone
    # when the controller is unreachable - force it closed unless explicitly
    # overridden.
    ARGS="$ARGS --verify-client-url=$VERIFY_CLIENT_URL --verify-client-url-fail-open=${VERIFY_CLIENT_FAIL_OPEN:-false}"

    # Optional convenience: wait for a controller advertised as host:port.
    if [ -n "$WAIT_FOR_CONTROLLER" ]; then
        i=0
        until wget -q -O /dev/null --timeout=2 "http://$WAIT_FOR_CONTROLLER/healthz" 2>/dev/null; do
            i=$((i+1))
            if [ $i -ge "${WAIT_FOR_CONTROLLER_TIMEOUT:-30}" ]; then
                echo "Warning: admission controller at $WAIT_FOR_CONTROLLER not reachable after ${WAIT_FOR_CONTROLLER_TIMEOUT}s; starting derper anyway (fail-open=${VERIFY_CLIENT_FAIL_OPEN:-false})."
                break
            fi
            sleep 1
        done
    fi
else
    echo "Warning: VERIFY_CLIENT_URL not set. No admission control - anyone who knows your address can relay through this server."
fi

echo "Starting DERP server, hostname: $DERP_HOSTNAME..."
# shellcheck disable=SC2086
exec derper --hostname="$DERP_HOSTNAME" $ARGS
