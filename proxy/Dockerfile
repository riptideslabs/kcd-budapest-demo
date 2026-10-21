# The binary is cross-compiled on the host (see scripts/images.sh).
FROM scratch
COPY egress-proxy /usr/local/bin/egress-proxy
ENV PATH=/usr/local/bin
USER 65532:65532
ENTRYPOINT ["egress-proxy"]
