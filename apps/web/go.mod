module github.com/s10akir/discord-archiver-go/apps/web

go 1.26.4

require (
	github.com/bwmarrin/discordgo v0.29.0
	github.com/s10akir/discord-archiver-go/pkg/archiveformat v0.0.0
	github.com/yuin/goldmark v1.7.13
)

require (
	github.com/gorilla/websocket v1.4.2 // indirect
	golang.org/x/crypto v0.0.0-20210421170649-83a5a9bb288b // indirect
	golang.org/x/sys v0.0.0-20201119102817-f84b799fce68 // indirect
)

replace github.com/s10akir/discord-archiver-go/pkg/archiveformat => ../../pkg/archiveformat
