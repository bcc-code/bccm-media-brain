FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /mediabrain ./cmd/mediabrain

# alpine rather than distroless: the compose healthcheck needs wget.
FROM alpine:3
RUN apk add --no-cache ca-certificates && adduser -D -H -u 10001 mediabrain
COPY --from=build /mediabrain /mediabrain
USER mediabrain
EXPOSE 8080
ENTRYPOINT ["/mediabrain"]
CMD ["serve"]
