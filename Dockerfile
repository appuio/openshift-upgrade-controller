FROM docker.io/library/alpine:3.17 as runtime

RUN \
  apk add --update --no-cache \
    bash \
    curl \
    ca-certificates \
    tzdata

ENTRYPOINT ["ocp-drain-monitor"]
COPY ocp-drain-monitor /usr/bin/

USER 65536:0
