# The binary is cross-compiled on the host (see scripts/images.sh).
FROM scratch
COPY egress-injector /usr/local/bin/egress-injector
ENV PATH=/usr/local/bin
USER 65532:65532
ENTRYPOINT ["egress-injector"]
