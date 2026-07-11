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

## Commit Messages

Commit messages must consist of exactly one line in the following format:

```text
tag: 日本語で簡潔にコミット内容を説明する
```

Use one of the following tags:

- `feat`: 機能の追加または修正を伴うコミット
- `docs`: ドキュメントのみを変更するコミット
- `test`: テストコードのみを変更するコミット
- `refact`: リファクタリングのみを行うコミット
- `chore`: 上記のいずれにも該当しないコミット（例: コンフィグや開発周辺ツールの更新）

If it is difficult to determine which tag applies, use `feat`.
