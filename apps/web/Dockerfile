FROM golang:1.26-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /discord-archive-web ./apps/web/cmd/discord-archive-web

FROM alpine:3.22
WORKDIR /data
COPY --from=build /discord-archive-web /usr/local/bin/discord-archive-web
VOLUME ["/data/archive"]
ENTRYPOINT ["discord-archive-web"]
