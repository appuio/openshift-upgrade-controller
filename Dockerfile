FROM gcr.io/distroless/static:nonroot

ENTRYPOINT ["/usr/bin/openshift-upgrade-controller"]
COPY openshift-upgrade-controller /usr/bin/
