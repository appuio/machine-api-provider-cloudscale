FROM docker.io/library/alpine:3.23

RUN \
  apk add --update --no-cache \
    bash \
    curl \
    ca-certificates \
    tzdata

ENTRYPOINT ["machine-api-provider-cloudscale"]
COPY machine-api-provider-cloudscale /usr/bin/

USER 65536:0
