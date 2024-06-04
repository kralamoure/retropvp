FROM golang:1.16.2-buster AS builder

RUN git config --global credential.helper store
COPY .git-credentials /root/.git-credentials

WORKDIR /app
COPY . .

RUN go env -w GOPRIVATE=github.com/kralamoure,gitlab.com/dofuspro
RUN go install -v ./...

FROM ubuntu:20.04

LABEL org.opencontainers.image.source="https://github.com/kralamoure/retropvp"

RUN apt-get update && apt-get install -y

WORKDIR /app
COPY --from=builder /go/bin/ .

ENTRYPOINT ["./retropvp"]
