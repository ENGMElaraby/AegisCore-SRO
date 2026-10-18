FROM golang:1.26.5 AS builder
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY framework ./framework
COPY cmd/vfilter-supervisor ./cmd/vfilter-supervisor

RUN go test ./cmd/vfilter-supervisor
RUN GOOS=linux CGO_ENABLED=0 go build -trimpath -o /app/vfilter-supervisor ./cmd/vfilter-supervisor

FROM alpine:3.22
RUN apk --no-cache add ca-certificates
WORKDIR /etc/aegiscore
COPY --from=builder /app/vfilter-supervisor ./vfilter-supervisor

# Runs as root: the mounted Docker socket is owned by root on the host.
ENTRYPOINT ["./vfilter-supervisor"]
