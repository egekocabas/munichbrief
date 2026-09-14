# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.27.1-bookworm@sha256:648f440f42a0958804efb24df176f806f9d353b41f1c0627f666428e40310f6b AS build

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
COPY LICENSES ./LICENSES
COPY LICENSE THIRD_PARTY_NOTICES.md ./
# The reviewed runtime/tzdata notices must match the toolchain used to build.
RUN cmp /usr/local/go/LICENSE LICENSES/go-4-0.txt && \
    cmp /usr/share/doc/tzdata/copyright LICENSES/debian-tzdata.txt
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.buildCommit=${COMMIT_SHA} -X main.buildTime=${BUILD_TIME}" \
    -o /out/munichbrief ./cmd/munichbrief
RUN install -d -o 65532 -g 65532 /out/data /out/tmp

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

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
COPY --from=build /src/LICENSE /usr/share/munichbrief/LICENSE
COPY --from=build /src/THIRD_PARTY_NOTICES.md /usr/share/munichbrief/THIRD_PARTY_NOTICES.md
COPY --from=build /src/LICENSES /usr/share/munichbrief/LICENSES
COPY --from=build --chown=65532:65532 /out/data /data
COPY --from=build --chown=65532:65532 /out/tmp /tmp

USER 65532:65532
EXPOSE 8080 9090
VOLUME ["/data"]
ENTRYPOINT ["/munichbrief"]
CMD ["serve"]
