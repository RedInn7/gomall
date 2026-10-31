FROM golang:1.26 AS builder

ENV GO111MODULE=on \
    GOPROXY=https://goproxy.cn,direct

WORKDIR /app
# 先只拷依赖清单，源码改动时可以复用依赖下载这一层缓存
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o main ./cmd
# 路由从 ./web/dist 托管店面，产物要落在 publish/web/dist
RUN mkdir -p publish/web \
    && cp main publish \
    && cp -r config static publish \
    && cp -r web/dist publish/web

FROM alpine:3.24

# Stripe、Xcash、embedding 等 HTTPS 调用需要 CA 证书
RUN apk add --no-cache ca-certificates

WORKDIR /app

COPY --from=builder /app/publish .

# 指定运行时环境变量
ENV GIN_MODE=release
EXPOSE 5003

ENTRYPOINT ["./main"]
