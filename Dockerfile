FROM golang:1.11-alpine as builder

WORKDIR /go/src/github.com/flocasts/rtmp-server

ADD main.go main.go
ADD Gopkg.toml Gopkg.toml
ADD Gopkg.lock Gopkg.lock

RUN apk update && apk add dep ca-certificates && rm -rf /var/cache/apk/* && apk add git

RUN dep ensure

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o rtmp-server

FROM scratch

WORKDIR /app

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /go/src/github.com/flocasts/rtmp-server/rtmp-server ./rtmp-server

EXPOSE 1935
EXPOSE 8080

ENTRYPOINT ["/app/rtmp-server"]