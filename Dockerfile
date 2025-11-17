FROM golang:1.25-alpine AS builder

RUN apk add --no-cache build-base

WORKDIR /src

RUN go install tailscale.com/cmd/derper@latest

RUN go install tailscale.com/cmd/tailscaled@latest

FROM alpine:latest

RUN apk add --no-cache ca-certificates iproute2

COPY --from=builder /go/bin/derper /usr/bin/derper
COPY --from=builder /go/bin/tailscaled /usr/bin/tailscaled

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

VOLUME /var/lib/derper

RUN mkdir -p /var/run/tailscale

EXPOSE 40007/tcp
EXPOSE 40008/udp
ENV DERP_PORT=40007
ENV STUN_PORT=40008

ENTRYPOINT ["/entrypoint.sh"]