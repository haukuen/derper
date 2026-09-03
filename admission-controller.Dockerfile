FROM golang:1.27-alpine AS builder

WORKDIR /src

ARG VERSION=dev

COPY go.mod ./
COPY cmd ./cmd
RUN CGO_ENABLED=0 go build -ldflags "-X main.version=${VERSION}" -o /out/admission-controller ./cmd/admission-controller

FROM alpine:3.24

# Non-root: the controller only reads the whitelist bind mount and
# listens on 8081.
RUN addgroup -S admission \
    && adduser -S -G admission admission \
    && mkdir -p /etc/derper

COPY --from=builder /out/admission-controller /usr/bin/admission-controller

EXPOSE 8081/tcp

ENV ADMIT_LISTEN=:8081 \
    ADMIT_WHITELIST=/etc/derper/whitelist.txt

USER admission

ENTRYPOINT ["/usr/bin/admission-controller"]
