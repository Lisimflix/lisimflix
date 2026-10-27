# M1 direct-play image: amd64, non-root, no shell and no encoder.
# Build with `make image`. Bases are pinned by digest.

FROM node:26.12.1-trixie@sha256:a98e6fda54fdbb22d4238054cca9a2a113bcd1191c09193a535aae22f400aa8e AS web
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

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/lisimflix /usr/local/bin/lisimflix
USER 65532:65532
EXPOSE 8420
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/usr/local/bin/lisimflix", "healthcheck"]
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/lisimflix"]
CMD ["serve", "--config", "/config/m1.json"]
