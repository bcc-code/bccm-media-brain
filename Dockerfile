FROM node:24-alpine AS web
WORKDIR /web
RUN npm install -g pnpm@12.3.4
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
# The postinstall script needs the sources; the build prepares Nuxt itself.
RUN pnpm install --frozen-lockfile --ignore-scripts
COPY web/ ./
RUN pnpm build

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/.output/public internal/webui/dist
RUN CGO_ENABLED=0 go build -tags webui -trimpath -ldflags="-s -w" -o /mediabrain ./cmd/mediabrain

# alpine rather than distroless: the compose healthcheck needs wget.
FROM alpine:3
RUN apk add --no-cache ca-certificates && adduser -D -H -u 10001 mediabrain
COPY --from=build /mediabrain /mediabrain
USER mediabrain
EXPOSE 8080
ENTRYPOINT ["/mediabrain"]
CMD ["serve"]
