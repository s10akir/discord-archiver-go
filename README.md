# discord-archiver-go

Discord bot tokenを使って、指定したDiscordサーバーの見えるチャンネルとメッセージをJSONLに書き出します。

Archiver、PostgreSQLを正本とするWebアプリ、JSONLをHTTP同期するImporterを独立したGoアプリとして管理するmonorepoです。

```text
apps/
  archiver/       Discordからアーカイブを作成するアプリ
  web/            PostgreSQL上のアーカイブを閲覧・検索するWebアプリ
  importer/       JSONLを検出してWebへ同期する外部アプリ
pkg/
  archiveformat/  両アプリが共有する保存形式の契約
```

## Usage

Botを対象サーバーに参加させ、対象チャンネルで `View Channel` と `Read Message History` を付与してください。メッセージ本文、添付、埋め込みなどの本文系フィールドを取得するには、Discord Developer Portalで `MESSAGE_CONTENT` privileged intent も有効化します。

`.env` を置くと自動で読み込みます。

```dotenv
DISCORD_BOT_TOKEN=your-bot-token
DISCORD_GUILD_ID=your-guild-id
```

オプション無しで起動するとデーモンとして常駐します。デフォルトでは起動直後にスケジュール用タイムゾーン基準の前日分をdumpし、その後は毎日指定時刻に前日分をdumpします。

```bash
go run ./apps/archiver/cmd/discord-archiver -out-dir archive
```

実行時刻とタイムゾーンは環境変数またはフラグで指定できます。フラグの指定が環境変数より優先されます。

```dotenv
TZ=Asia/Tokyo
DISCORD_ARCHIVER_SCHEDULE_TIME=03:00
DISCORD_ARCHIVER_RUN_ON_START=true
```

```bash
go run ./apps/archiver/cmd/discord-archiver \
  -out-dir archive \
  -schedule-time 03:00 \
  -timezone Asia/Tokyo
```

起動直後の前日分dumpを回避する場合は `-no-run-on-start` を付けるか、`DISCORD_ARCHIVER_RUN_ON_START=false` を設定します。

```bash
go run ./apps/archiver/cmd/discord-archiver -out-dir archive -no-run-on-start
```

手動で全期間を取得する場合:

```bash
go run ./apps/archiver/cmd/discord-archiver dump -all -out-dir archive
```

手動で指定したJST日付だけを洗い替える場合:

```bash
go run ./apps/archiver/cmd/discord-archiver dump -date 2026-07-09 -out-dir archive
```

フラグでもBot tokenとguild IDを指定できます。

```bash
go run ./apps/archiver/cmd/discord-archiver \
  -token 'your-bot-token' \
  -guild 'your-guild-id' \
  -out-dir archive
```

デフォルトでは通常チャンネルに加えて、アクティブスレッド、公開アーカイブ済みスレッド、Botから見える非公開アーカイブ済みスレッドも取得します。非公開アーカイブ済みスレッドを除外する場合は `-no-private-threads` を付けます。

```bash
go run ./apps/archiver/cmd/discord-archiver -out-dir archive -no-private-threads
```

デフォルトではメッセージの添付ファイルも実体をダウンロードして保存します。再実行時、既に同じサイズのファイルが保存済みであれば再ダウンロードしません。取得しない場合は `-attachments=false` を付けるか、`DISCORD_ARCHIVER_DOWNLOAD_ATTACHMENTS=false` を設定します。

```bash
go run ./apps/archiver/cmd/discord-archiver -out-dir archive -attachments=false
```

## Web

アーカイブ済みデータをPostgreSQLから閲覧・検索するWebアプリです。添付ファイルの実体だけをarchiveディレクトリからread-onlyで配信します。

開発・動作確認ではDocker Composeを使います。Web単体を更新する場合も、Compose経由でイメージを再ビルドします。

```bash
docker compose up -d --build discord-archive-web discord-archive-importer
```

`http://localhost:8080/` ではギルド、チャンネル/スレッド、メッセージ、メディアを閲覧できます。`/search` では本文・投稿者・添付名・embedの部分一致と、guild、channel、期間、添付、メディア種別による絞り込みを利用できます。

Web UIはReact、Vite、Tailwind CSS、shadcn/uiをベースにしています。本番用アセットはWebイメージのビルド中に生成され、Goバイナリへ埋め込まれます。生成先の`apps/web/internal/web/frontend`はGitおよびDockerのビルドコンテキストでは管理しません。

```dotenv
DISCORD_ARCHIVE_WEB_ADDR=:8080
DATABASE_URL=postgres://discord_archive:discord_archive@localhost:5432/discord_archive?sslmode=disable
```

## Importer

Importerは内容ハッシュが変わったmetadataと日付パーティションを定期的にWebの更新APIへ送ります。同期済みのハッシュは `-state-file` で指定したJSONへ永続化され、再起動後も変更のないデータは再送しません。省略時は archive 直下の `.discord-archive-importer-state.json` を使用します。全件を再同期する場合はImporterを停止し、この状態ファイルを削除してから起動します。PostgreSQLへは接続しません。

```bash
DISCORD_ARCHIVE_WEB_URL=http://localhost:8080 \
  go run ./apps/importer/cmd/discord-archive-importer -archive-dir archive -interval 30s
```

## Development

ルートの `go.work` に4つのmoduleを登録しています。Webは埋め込み用フロントエンドアセットをイメージ内で生成するため、開発環境ではネイティブにビルドせずDocker Composeを使います。

```bash
GOCACHE=/tmp/go-build-cache GOMODCACHE=/tmp/go-mod-cache \
  go test ./apps/archiver/... ./apps/importer/... ./pkg/archiveformat/...

GOCACHE=/tmp/go-build-cache GOMODCACHE=/tmp/go-mod-cache \
  go build -o /tmp/discord-archiver ./apps/archiver/cmd/discord-archiver

GOCACHE=/tmp/go-build-cache GOMODCACHE=/tmp/go-mod-cache \
  go build -o /tmp/discord-archive-importer ./apps/importer/cmd/discord-archive-importer

docker compose build discord-archive-web
```

## Docker

`.env` にDiscordの接続情報を設定します。このファイルはGitの追跡対象外です。

```dotenv
DISCORD_BOT_TOKEN=your-bot-token
DISCORD_GUILD_ID=your-guild-id
```

ComposeでArchiver、PostgreSQL、Web、Importerを起動します。Archiverはarchiveへ書き込み、WebとImporterはread-onlyで共有します。ImporterからWebへの更新APIはCompose内部ネットワークだけで利用します。

```bash
docker compose up -d --build
```

Webはホストの8080番ポートで公開されます。

各アプリのイメージを個別にビルドする場合:

```bash
docker build -f apps/archiver/Dockerfile -t discord-archiver .
docker build -f apps/web/Dockerfile -t discord-archive-web .
docker build -f apps/importer/Dockerfile -t discord-archive-importer .
```

`main` ブランチが更新されると、GitHub Actionsがamd64/arm64向けのイメージをGHCRへ公開します。

```bash
docker pull ghcr.io/s10akir/discord-archiver:latest
docker pull ghcr.io/s10akir/discord-archive-web:latest
docker pull ghcr.io/s10akir/discord-archive-importer:latest
```

各イメージには `latest` に加えて、公開元のコミットを特定できる `sha-<短縮コミットSHA>` タグが付きます。Pull Requestではイメージを公開せず、両プラットフォーム向けのビルドだけを検証します。

Archiverだけを直接起動する場合:

```bash
docker run --rm \
  -e DISCORD_BOT_TOKEN \
  -e DISCORD_GUILD_ID \
  -e TZ=Asia/Tokyo \
  -e DISCORD_ARCHIVER_SCHEDULE_TIME=03:00 \
  -v "$PWD/archive:/data/archive" \
  discord-archiver -out-dir /data/archive
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
          attachments/
            <message_id>/
              <attachment_id>-<filename>
```

`date=YYYY-MM-DD` はJST基準のメッセージ作成日です。JSONL内の `message.timestamp` はdiscordgoが受け取ったDiscord APIの値をそのまま保存します。

`messages.jsonl` は1メッセージ1行です。各行には `guild_id`、`channel_id`、`channel_name`、`channel_type`、`parent_id`、discordgoの `message` オブジェクトが入ります。添付ファイルの実体は同じ行の `message.attachments` にあるIDとファイル名から `attachments/<message_id>/<attachment_id>-<filename>` として組み立てられるパスに保存されます。
