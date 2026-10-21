# The binary is built on the host and copied into the image.
#
#   make build          # CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ... -o lifemail
#   make image          # docker build -t lifemail:latest .
#
# The container runs as root, so a bind-mounted /data does not need any
# ownership changes on the host.

FROM alpine:3.22@sha256:5291449c3df73caf6ed85e649dec1b9e818b39a5d8c871e97afc13e9cd5e8fa8

RUN apk add --no-cache ca-certificates

COPY lifemail /usr/local/bin/lifemail

ARG VERSION=dev
LABEL org.opencontainers.image.version=$VERSION

VOLUME ["/data"]
WORKDIR /data
EXPOSE 8080 993 465

# /healthz needs no authentication. Override the address when lifemail runs on
# a non-default port: docker build ... && docker run --health-cmd ...
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/lifemail", "serve"]
