# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.26.6-bookworm AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT_SHA=dev
ARG BUILD_TIME=dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.buildCommit=${COMMIT_SHA} -X main.buildTime=${BUILD_TIME}" \
    -o /out/munichbrief ./cmd/munichbrief
RUN install -d -o 65532 -g 65532 /out/data /out/tmp

FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=dev
ARG COMMIT_SHA=dev
ARG BUILD_TIME=dev
LABEL org.opencontainers.image.title="MunichBrief" \
      org.opencontainers.image.description="Local-first reader for Munich Police press releases" \
      org.opencontainers.image.url="https://github.com/egekocabas/munichbrief" \
      org.opencontainers.image.source="https://github.com/egekocabas/munichbrief" \
      org.opencontainers.image.documentation="https://github.com/egekocabas/munichbrief#readme" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.vendor="MunichBrief" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$COMMIT_SHA" \
      org.opencontainers.image.created="$BUILD_TIME"

COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=build /out/munichbrief /munichbrief
COPY --from=build --chown=65532:65532 /out/data /data
COPY --from=build --chown=65532:65532 /out/tmp /tmp

USER 65532:65532
EXPOSE 8080 9090
VOLUME ["/data"]
ENTRYPOINT ["/munichbrief"]
CMD ["serve"]
