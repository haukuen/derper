FROM golang:1.27-alpine AS builder

WORKDIR /src

COPY go.mod ./
COPY cmd ./cmd
RUN CGO_ENABLED=0 go build -o /out/admission-controller ./cmd/admission-controller

FROM alpine:latest

RUN mkdir -p /etc/derper

COPY --from=builder /out/admission-controller /usr/bin/admission-controller

EXPOSE 8081/tcp

ENV ADMIT_LISTEN=:8081 \
    ADMIT_WHITELIST=/etc/derper/whitelist.txt

ENTRYPOINT ["/usr/bin/admission-controller"]
