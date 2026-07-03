# AGENTS.md

## Go Commands

Some AI agent sandboxes may not allow writes under the home directory. When
running Go commands in such environments, use cache directories under `/tmp`:

```bash
GOCACHE=/tmp/go-build-cache \
GOMODCACHE=/tmp/go-mod-cache \
go test ./...
```

For building the CLI binary:

```bash
GOCACHE=/tmp/go-build-cache \
GOMODCACHE=/tmp/go-mod-cache \
go build -o /tmp/discord-archiver ./cmd/discord-archiver
```
