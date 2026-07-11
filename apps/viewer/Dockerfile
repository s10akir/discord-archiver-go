FROM golang:1.26-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /discord-archive-viewer ./apps/viewer/cmd/discord-archive-viewer

FROM alpine:3.22
WORKDIR /data
COPY --from=build /discord-archive-viewer /usr/local/bin/discord-archive-viewer
VOLUME ["/data/archive"]
ENTRYPOINT ["discord-archive-viewer"]
