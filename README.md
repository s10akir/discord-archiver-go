# discord-archiver-go

Discord bot tokenを使って、指定したDiscordサーバーの見えるチャンネルとメッセージをJSONLに書き出します。

## Usage

Botを対象サーバーに参加させ、対象チャンネルで `View Channel` と `Read Message History` を付与してください。メッセージ本文、添付、埋め込みなどの本文系フィールドを取得するには、Discord Developer Portalで `MESSAGE_CONTENT` privileged intent も有効化します。

`.env` を置くと自動で読み込みます。

```dotenv
DISCORD_BOT_TOKEN=your-bot-token
DISCORD_GUILD_ID=your-guild-id
```

オプション無しで起動するとデーモンとして常駐します。デフォルトでは起動直後にスケジュール用タイムゾーン基準の前日分をdumpし、その後は毎日指定時刻に前日分をdumpします。

```bash
go run ./cmd/discord-archiver -out-dir archive
```

実行時刻とタイムゾーンは環境変数またはフラグで指定できます。フラグの指定が環境変数より優先されます。

```dotenv
TZ=Asia/Tokyo
DISCORD_ARCHIVER_SCHEDULE_TIME=03:00
DISCORD_ARCHIVER_RUN_ON_START=true
```

```bash
go run ./cmd/discord-archiver \
  -out-dir archive \
  -schedule-time 03:00 \
  -timezone Asia/Tokyo
```

起動直後の前日分dumpを回避する場合は `-no-run-on-start` を付けるか、`DISCORD_ARCHIVER_RUN_ON_START=false` を設定します。

```bash
go run ./cmd/discord-archiver -out-dir archive -no-run-on-start
```

手動で全期間を取得する場合:

```bash
go run ./cmd/discord-archiver dump -all -out-dir archive
```

手動で指定したJST日付だけを洗い替える場合:

```bash
go run ./cmd/discord-archiver dump -date 2026-07-09 -out-dir archive
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

## Docker

イメージをビルドして起動すると、コンテナ内でデーモンとして常駐します。

```bash
docker build -t discord-archiver .
docker run --rm \
  -e DISCORD_BOT_TOKEN \
  -e DISCORD_GUILD_ID \
  -e TZ=Asia/Tokyo \
  -e DISCORD_ARCHIVER_SCHEDULE_TIME=03:00 \
  -v "$PWD/archive:/data/archive" \
  discord-archiver -out-dir /data/archive
```

`compose.yaml.example` も同じ設定例です。

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
