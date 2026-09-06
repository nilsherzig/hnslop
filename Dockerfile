FROM golang:1.24-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /hnslop ./cmd/hnslop

FROM alpine:3.21

RUN apk add --no-cache ca-certificates
COPY --from=builder /hnslop /usr/local/bin/hnslop

WORKDIR /data

EXPOSE 8000

ENV HNSLOP_DATABASE_PATH=/data/hnslop.sqlite3
ENTRYPOINT ["/usr/local/bin/hnslop"]
CMD ["serve", "--host", "0.0.0.0", "--port", "8000"]
