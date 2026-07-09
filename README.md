# discord-archiver-go

Discord bot tokenを使って、指定したDiscordサーバーの見えるチャンネルとメッセージをJSONLに書き出します。

## Usage

Botを対象サーバーに参加させ、対象チャンネルで `View Channel` と `Read Message History` を付与してください。メッセージ本文、添付、埋め込みなどの本文系フィールドを取得するには、Discord Developer Portalで `MESSAGE_CONTENT` privileged intent も有効化します。

`.env` を置くと自動で読み込みます。

```dotenv
DISCORD_BOT_TOKEN=your-bot-token
DISCORD_GUILD_ID=your-guild-id
```

```bash
go run ./cmd/discord-archiver -out discord-archive.jsonl
```

フラグでも指定できます。

```bash
go run ./cmd/discord-archiver \
  -token 'your-bot-token' \
  -guild 'your-guild-id' \
  -out discord-archive.jsonl
```

デフォルトでは通常チャンネルに加えて、アクティブスレッド、公開アーカイブ済みスレッド、Botから見える非公開アーカイブ済みスレッドも取得します。非公開アーカイブ済みスレッドを除外する場合は `-no-private-threads` を付けます。

```bash
go run ./cmd/discord-archiver -no-private-threads -out discord-archive.jsonl
```

## Output

出力は1メッセージ1行のJSONLです。各行には `guild_id`、`channel_id`、`channel_name`、`channel_type`、`parent_id`、discordgoの `message` オブジェクトが入ります。
