# retropvp

[![CI](https://github.com/kralamoure/retropvp/actions/workflows/ci.yml/badge.svg)](https://github.com/kralamoure/retropvp/actions/workflows/ci.yml)

`retropvp` is an unofficial PVP game server for Dofus Retro.

## Requirements

- [Git](https://git-scm.com/)
- [Go](https://golang.org/)

## Build

```sh
git clone https://github.com/kralamoure/retropvp
cd retropvp
go build ./cmd/...
```

## Installation

```sh
go get -u -v github.com/kralamoure/retropvp/...
```

## Usage

```sh
retropvp --help
```

### Output

```text
retropvp is an unofficial PVP game server for Dofus Retro.

Find more information at: https://github.com/kralamoure/retropvp

Options:
  -h, --help               Print usage information
  -d, --debug              Enable debug mode
  -i, --id int             Server ID
  -a, --address string     Server listener address (default "0.0.0.0:5555")
  -p, --postgres string    PostgreSQL connection string (default "postgresql://user:password@host/database")
  -t, --timeout duration   Connection timeout (default 30m0s)

Usage: retropvp [options]
```
