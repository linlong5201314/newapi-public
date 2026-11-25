FROM oven/bun:1.4.4@sha256:1d22548f177f4ff38e0c15baa3c2c46c6c2060ba2d1bc573bb4720b407548f1d AS builder

WORKDIR /build/web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY ./web ./
COPY ./VERSION /build/VERSION
RUN DISABLE_ESLINT_PLUGIN='true' VITE_REACT_APP_VERSION=$(cat /build/VERSION) bun run build

FROM golang:1.26.10-alpine@sha256:9ce231b28998b32e116927c61aa5c406183061d4f3f2d61b292c88e7f6bdf7ca AS builder2
ENV GO111MODULE=on CGO_ENABLED=0 GOWORK=off

ARG TARGETOS
ARG TARGETARCH
ENV GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64}
ENV GOEXPERIMENT=greenteagc

WORKDIR /build

ADD go.mod go.sum ./
# relaykit and tokenkit are local submodules referenced via replace; their
# go.mod files must be present for go mod download to resolve the main module graph.
ADD relaykit/go.mod ./relaykit/go.mod
ADD tokenkit/go.mod ./tokenkit/go.mod
RUN go mod download

COPY . .
COPY --from=builder /build/web/dist ./web/dist
RUN go build -pgo="$(go list -m -f '{{.Dir}}' github.com/Calcium-Ion/moejs)/default.pgo" -ldflags "-s -w -X 'github.com/QuantumNous/new-api/common.Version=$(cat VERSION)'" -o new-api

FROM debian:bookworm-slim@sha256:f06537653ac770703bc45b4b113475bd402f451e85223f0f2837acbf89ab020a

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata curl wget gzip \
    && rm -rf /var/lib/apt/lists/* \
    && update-ca-certificates \
    && groupadd --system --gid 10001 newapi && useradd --system --uid 10001 --gid newapi --home-dir /data newapi \
    && mkdir -p /data && chown newapi:newapi /data \
    && wget -qO /tmp/mihomo.gz "https://github.com/MetaCubeX/mihomo/releases/download/v1.19.19/mihomo-linux-amd64-compatible-v1.19.19.gz" \
    && printf '%s  %s\n' '45fbee22e63ec27319573efe1ccc8aa829c3f98c16ffc94a2d5ea86531e358b0' '/tmp/mihomo.gz' | sha256sum -c - \
    && gzip -d /tmp/mihomo.gz && mv /tmp/mihomo /usr/local/bin/mihomo && chmod 0755 /usr/local/bin/mihomo

COPY --from=builder2 /build/new-api /new-api
COPY LICENSE NOTICE THIRD-PARTY-LICENSES.md /licenses/

ENV PORT=3000 \
    TZ=Asia/Shanghai \
    SQLITE_PATH=/data/one-api.db \
    GIN_MODE=release

# CRYPTO_SECRET is mandatory in release mode; missing keys fail startup.
EXPOSE 3000
WORKDIR /data
USER newapi:newapi
HEALTHCHECK --interval=30s --timeout=5s --start-period=40s --retries=3 \
    CMD curl -fsS "http://127.0.0.1:${PORT:-3000}/api/status" >/dev/null || exit 1
ENTRYPOINT ["/new-api"]
