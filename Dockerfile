ARG TARGETOS=linux
ARG TARGETARCH=amd64

FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETOS=linux
ARG TARGETARCH=amd64
COPY bin/cluster-autoheal-${TARGETOS}-${TARGETARCH} /cluster-autoheal
USER 65532:65532
ENTRYPOINT ["/cluster-autoheal"]
