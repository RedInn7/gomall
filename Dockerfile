FROM golang:1.25 AS builder

ENV GO111MODULE=on \
    GOPROXY=https://goproxy.cn,direct

WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o main ./cmd
WORKDIR /app
RUN mkdir publish  \
    && cp main publish  \
    && cp -r config web/dist static publish

FROM busybox:1.28.4

WORKDIR /app

COPY --from=builder /app/publish .

# 指定运行时环境变量
ENV GIN_MODE=release
EXPOSE 5003

ENTRYPOINT ["./main"]
