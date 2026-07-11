FROM golang:1.26-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /discord-archiver ./apps/archiver/cmd/discord-archiver

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /data
COPY --from=build /discord-archiver /usr/local/bin/discord-archiver
ENV TZ=Asia/Tokyo \
    DISCORD_ARCHIVER_SCHEDULE_TIME=03:00
ENTRYPOINT ["discord-archiver"]
