FROM golang:1.26-alpine AS build
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ping ./cmd/ping

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && \
    adduser -D -u 10001 ping
USER ping
COPY --from=build /out/ping /usr/local/bin/ping
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/ping"]
