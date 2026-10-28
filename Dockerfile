# syntax=docker/dockerfile:1.17
# Lisimflix image: amd64, non-root, no shell. Ships ffprobe (LIB-1), no encoder yet.
# Build with `make image`. Bases are pinned by digest.

FROM node:24.22.1-trixie@sha256:016f604a11c4c725f0a4c62e29ff11adab153d297bf97046c9bf2ddb9f1011d3 AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.27.3-trixie@sha256:fbb7ae40b62ff8afe31662df570d39b4f5052ac32b90dae8e60d7657a20a74f4 AS build
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
COPY --from=web /src/web/dist/ ./internal/webassets/dist/
ARG VERSION=dev
# TAGS=m1smoke adds a test-only migration, for upgrade smoke tests only.
ARG TAGS=
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -tags "$TAGS" \
      -ldflags="-s -w -X main.version=$VERSION" -o /out/lisimflix ./cmd/lisimflix

# jellyfin-ffmpeg portable build (GPLv3), used only as a separate process.
# Not tracked by Dependabot: to update, take the new release's
# portable_linux64-gpl asset and its sha256 from
# https://github.com/jellyfin/jellyfin-ffmpeg/releases and change both lines,
# and the version and source links in NOTICE.md (GPLv3 source pointer, #63).
FROM scratch AS ffmpeg
ADD --unpack=true --checksum=sha256:b86dc023c64a5a9a410e6e3a938a970460214fae78f31c1553c9f88f1c289b6a \
    https://github.com/jellyfin/jellyfin-ffmpeg/releases/download/v8.1.3-1/jellyfin-ffmpeg_8.1.3-1_portable_linux64-gpl.tar.xz /
ADD --checksum=sha256:8ceb4b9ee5adedde47b31e975c1d90c73ad27b6b165a1dcd80c7c545eb65b903 \
    https://raw.githubusercontent.com/jellyfin/jellyfin-ffmpeg/v8.1.3-1/COPYING.GPLv3 /COPYING.GPLv3
ADD --checksum=sha256:2e1d16c72fd74e12063776371da757322f8b77589386532f4fd8634bde7de1af \
    https://raw.githubusercontent.com/jellyfin/jellyfin-ffmpeg/v8.1.3-1/LICENSE.md /LICENSE.md

# base (not static): the ffprobe build links against glibc.
FROM gcr.io/distroless/base-debian12:nonroot@sha256:7f0c72cd138b442ae0deeb69c08b1acf5525439ba251a49ad93c320a061567e5
COPY --from=build /out/lisimflix /usr/local/bin/lisimflix
COPY --from=ffmpeg /ffprobe /usr/lib/jellyfin-ffmpeg/ffprobe
COPY --from=ffmpeg /COPYING.GPLv3 /LICENSE.md /usr/share/doc/jellyfin-ffmpeg/
COPY NOTICE.md LICENSE /usr/share/doc/lisimflix/
USER 65532:65532
EXPOSE 8420
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/usr/local/bin/lisimflix", "healthcheck"]
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/lisimflix"]
CMD ["serve", "--config", "/config/m1.json"]
