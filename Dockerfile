FROM docker.io/library/alpine:3.20 as runtime

RUN \
  apk add --update --no-cache \
    bash \
    curl \
    ca-certificates \
    tzdata

ENTRYPOINT ["openshift-upgrade-controller"]
COPY openshift-upgrade-controller /usr/bin/

USER 65536:0
