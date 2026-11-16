# ?????????xhx2api??????????????????????? config/accounts
FROM golang:1.24-bookworm AS build
RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates && rm -rf /var/lib/apt/lists/*
RUN git clone --depth 1 https://github.com/haoxinyu1/xiaohuanxiong2api /up
WORKDIR /up/src
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags "-s -w" -o /out/xhx2api ./cmd/xhx2api

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata jq && rm -rf /var/lib/apt/lists/*
RUN useradd -r -u 10001 -m app
WORKDIR /app
COPY --from=build /out/xhx2api /app/xhx2api
COPY entrypoint.sh /app/entrypoint.sh
COPY config.template.json /app/config.template.json
RUN chmod +x /app/entrypoint.sh && mkdir -p /app/data && chown -R app:app /app
ENV TZ=Asia/Shanghai PORT=3066
EXPOSE 3066
ENTRYPOINT ["/app/entrypoint.sh"]

