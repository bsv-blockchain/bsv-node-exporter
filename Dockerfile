FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/bsv-node-exporter /bsv-node-exporter
USER 65532:65532
EXPOSE 9480
ENTRYPOINT ["/bsv-node-exporter"]
