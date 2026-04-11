# Installation And Onboarding

This guide covers the fastest way to build Lele, initialize its local state, and start the main services.

## Requirements

- Go toolchain compatible with the project
- `make`
- `bun` for building the embedded web UI

## Build From Source

```bash
git clone https://github.com/xilistudios/lele.git
cd lele
make deps
make build
```

Build output:

- platform binary: `build/lele-<platform>-<arch>`
- convenience symlink: `build/lele`

`make build` also builds the embedded web assets and runs code generation.

## Install Locally

```bash
make install
```

Default install path:

```text
~/.local/bin/lele
```

## First-Time Setup

Run:

```bash
lele onboard
```

The onboarding flow can:

- create `~/.lele/config.json`
- create the default workspace templates
- enable the web UI
- enable the native channel automatically when web is enabled
- generate a pairing PIN
- start `gateway` and `web` immediately

## Default Local Layout

Lele stores data under:

```text
~/.lele/
```

Typical files and directories:

```text
~/.lele/
├── config.json
├── logs/
├── native_clients.json
├── tmp/
└── workspace/
```

## Start Services Manually

### Agent CLI

```bash
lele agent
lele agent -m "Summarize this repository"
```

### Gateway

```bash
lele gateway
```

### Web UI

```bash
lele web start
lele web status
lele web stop
```

## Web + Native Local Flow

Recommended local setup:

1. Run `lele onboard`
2. Enable the web UI
3. Generate a pairing PIN
4. Start `lele gateway`
5. Start `lele web start`
6. Open the local web app and pair using the PIN

## Useful Make Targets

```bash
make build
make build-all
make test
make fmt
make vet
make check
make clean
```

## Common Paths

- config: `~/.lele/config.json`
- logs: `~/.lele/logs`
- workspace: `~/.lele/workspace`
- web pid file: `~/.lele/web.pid`
- web log: `~/.lele/logs/web.log`

## Related Docs

- `docs/session-and-workspace.md`
- `docs/web-ui.md`
- `docs/client-api.md`
- `docs/troubleshooting.md`
