# discord-archiver-go

Discord bot tokenを使って、指定したDiscordサーバーの見えるチャンネルとメッセージをJSONLに書き出します。

## Usage

Botを対象サーバーに参加させ、対象チャンネルで `View Channel` と `Read Message History` を付与してください。メッセージ本文、添付、埋め込みなどの本文系フィールドを取得するには、Discord Developer Portalで `MESSAGE_CONTENT` privileged intent も有効化します。

`.env` を置くと自動で読み込みます。

```dotenv
DISCORD_BOT_TOKEN=your-bot-token
DISCORD_GUILD_ID=your-guild-id
```

全期間を取得する場合:

```bash
go run ./cmd/discord-archiver -out-dir archive
```

指定したJST日付だけを洗い替える場合:

```bash
go run ./cmd/discord-archiver -out-dir archive -date 2026-07-09
```

フラグでもBot tokenとguild IDを指定できます。

```bash
go run ./cmd/discord-archiver \
  -token 'your-bot-token' \
  -guild 'your-guild-id' \
  -out-dir archive
```

デフォルトでは通常チャンネルに加えて、アクティブスレッド、公開アーカイブ済みスレッド、Botから見える非公開アーカイブ済みスレッドも取得します。非公開アーカイブ済みスレッドを除外する場合は `-no-private-threads` を付けます。

```bash
go run ./cmd/discord-archiver -out-dir archive -no-private-threads
```

## Output

出力先のデフォルトは `archive` です。

```text
archive/
  guild_id=817806841718243360/
    metadata/
      channels.jsonl
      threads.jsonl
    messages/
      date=2026-07-09/
        channel_id=817806841718243361/
          messages.jsonl
```

`date=YYYY-MM-DD` はJST基準のメッセージ作成日です。JSONL内の `message.timestamp` はdiscordgoが受け取ったDiscord APIの値をそのまま保存します。

`messages.jsonl` は1メッセージ1行です。各行には `guild_id`、`channel_id`、`channel_name`、`channel_type`、`parent_id`、discordgoの `message` オブジェクトが入ります。
