FROM golang:1-alpine AS builder

RUN apk add --no-cache ca-certificates
WORKDIR /build/lwnfeed
COPY . /build/lwnfeed
ENV CGO_ENABLED=0
RUN go build -o /usr/bin/mauliasproxy

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/bin/mauliasproxy /usr/bin/mauliasproxy

VOLUME /data
WORKDIR /data
CMD ["/usr/bin/mauliasproxy"]
