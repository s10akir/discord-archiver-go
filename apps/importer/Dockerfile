FROM golang:1.26.4-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /discord-archive-importer ./apps/importer/cmd/discord-archive-importer

FROM alpine:3.22
RUN adduser -D -u 10001 app
COPY --from=build /discord-archive-importer /usr/local/bin/discord-archive-importer
USER app
ENTRYPOINT ["discord-archive-importer"]
