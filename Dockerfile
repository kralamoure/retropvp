FROM golang:1.22.4-bookworm AS builder

WORKDIR /app
COPY . .

RUN go install -v ./...

FROM ubuntu:24.04

LABEL org.opencontainers.image.source="https://github.com/kralamoure/retropvp"

RUN apt-get update && apt-get upgrade -y

WORKDIR /app
COPY --from=builder /go/bin/ .

ENTRYPOINT ["./retropvp"]
