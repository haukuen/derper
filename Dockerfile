ARG TS_VERSION=stable

FROM golang:1.27-alpine AS builder

ARG TS_VERSION

RUN apk add --no-cache build-base git


RUN go install tailscale.com/cmd/derper@${TS_VERSION}

FROM alpine:3.24

# Non-root: derper only listens on unprivileged ports (DERP tcp, STUN udp)
# and writes its self-signed certs into /var/lib/derper.
RUN apk add --no-cache ca-certificates \
    && addgroup -S derper \
    && adduser -S -G derper derper \
    && mkdir -p /var/lib/derper \
    && chown -R derper:derper /var/lib/derper

COPY --from=builder /go/bin/derper /usr/bin/derper

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

VOLUME /var/lib/derper

EXPOSE 40007/tcp 40008/udp

ENV DERP_PORT=40007 \
    STUN_PORT=40008

USER derper

ENTRYPOINT ["/entrypoint.sh"]
