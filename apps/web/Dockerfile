FROM node:24-alpine AS frontend
WORKDIR /src/apps/web/frontend
COPY apps/web/frontend/package.json apps/web/frontend/package-lock.json ./
RUN npm ci
COPY apps/web/frontend ./
RUN npm run build

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY . .
COPY --from=frontend /src/apps/web/internal/web/frontend ./apps/web/internal/web/frontend
RUN CGO_ENABLED=0 go build -o /discord-archive-web ./apps/web/cmd/discord-archive-web

FROM alpine:3.22
WORKDIR /data
COPY --from=build /discord-archive-web /usr/local/bin/discord-archive-web
VOLUME ["/data/archive"]
ENTRYPOINT ["discord-archive-web"]
