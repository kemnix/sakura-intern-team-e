FROM --platform=$BUILDPLATFORM golang:1.25.12-alpine3.24@sha256:56961d79ea8129efddcc0b8643fd8a5416b4e6228cfd477e3fd61deb2672c587 AS builder
WORKDIR /app

ENV CGO_ENABLED=0
ENV GOARCH=amd64
ENV GOOS=linux

COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o sakuravel ./cmd/api/

FROM alpine:3.24.1@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/sakuravel .
EXPOSE 8080
CMD ["./sakuravel"]
