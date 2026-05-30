FROM golang:1.23-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o locke-connector .

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -H -s /sbin/nologin locke-connector

COPY --from=builder /build/locke-connector /usr/local/bin/locke-connector

USER locke-connector
WORKDIR /data

ENTRYPOINT ["locke-connector"]
CMD ["run"]
