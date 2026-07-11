module github.com/s10akir/discord-archiver-go/apps/web

go 1.26.4

require (
	github.com/bwmarrin/discordgo v0.29.0
	github.com/s10akir/discord-archiver-go/pkg/archiveformat v0.0.0
	github.com/yuin/goldmark v1.7.13
)

require (
	github.com/gorilla/websocket v1.4.2 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.7.5 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/sync v0.13.0 // indirect
	golang.org/x/sys v0.32.0 // indirect
	golang.org/x/text v0.24.0 // indirect
)

replace github.com/s10akir/discord-archiver-go/pkg/archiveformat => ../../pkg/archiveformat
