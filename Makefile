# VERSION 写进 SkyWalking agent 的文件名和 main.version。取子模块的提交而不是主仓库的，
# 否则改完代码再 make build-agent 会找不到之前 make tools 编出的 agent。
# OS/ARCH 默认取本机 Go 环境，交叉编译时在命令行覆盖，如 make tools OS=linux ARCH=amd64
VERSION ?= $(shell git -C skywalking-go rev-parse --short HEAD)
OS ?= $(shell go env GOOS)
ARCH ?= $(shell go env GOARCH)

DIR := $(shell pwd)
OUTPUT = main

BINARY = skywalking-go-agent
TOOLS_PATH = $(DIR)/tools
AGENT_SOURCE_PATH = $(DIR)/skywalking-go/tools/go-agent
AGENT_PATH = $(TOOLS_PATH)/$(BINARY)-$(VERSION)-$(OS)-$(ARCH)
AGENT_CONFIG = $(DIR)/config/locales/agent.yaml

CONTAINER_NAME = gin_mall_server
IMAGE_NAME = gin_mall:3.0

GO = go
GO_BUILD = $(GO) build
GO_BUILD_FLAGS = -v
GO_BUILD_LDFLAGS = -X main.version=$(VERSION)

.DEFAULT_GOAL := run

.PHONY: tools
tools:
	mkdir -p $(TOOLS_PATH)
	cd $(AGENT_SOURCE_PATH) && make deps
	cd $(AGENT_SOURCE_PATH) && \
	GOOS=$(OS) GOARCH=$(ARCH) $(GO_BUILD) $(GO_BUILD_FLAGS) -ldflags "$(GO_BUILD_LDFLAGS)" -o $(TOOLS_PATH)/$(BINARY)-$(VERSION)-$(OS)-$(ARCH) ./cmd

.PHONY: run
run:
	@make build
	@SNOWFLAKE_ALLOW_DEFAULT=$${SNOWFLAKE_ALLOW_DEFAULT:-true} ./$(OUTPUT)

.PHONY: build
build:
	@echo "build project to ./$(OUTPUT)"
	$(GO_BUILD) -o ./$(OUTPUT) ./cmd

.PHONY: build-agent
build-agent:
	$(GO_BUILD) -toolexec="$(AGENT_PATH) -config $(AGENT_CONFIG)" -a -o ./$(OUTPUT) ./cmd

.PHONY: env-up		# 启动环境
env-up:
	docker compose up -d
	@echo "env start success"

.PHONY: env-down	# 关闭环境
env-down:
	docker compose down
	@echo "env stop success"

.PHONY: docker-up	# 以容器形式部署项目
docker-up:
	docker build \
	-t $(IMAGE_NAME) \
	-f ./Dockerfile \
	./
	docker run \
	-it \
	--name $(CONTAINER_NAME) \
	--network host \
	-e SNOWFLAKE_ALLOW_DEFAULT=true \
	-v $(DIR)/config/locales/config.yaml:/app/config/locales/config.yaml:ro \
	-d $(IMAGE_NAME)
	@echo "container run success at localhost:5003"

.PHONY: docker-down # 结束docker部署,同时删除容器和镜像
docker-down:
	docker stop $(CONTAINER_NAME)
	docker rm $(CONTAINER_NAME)
	docker rmi $(IMAGE_NAME)
	@echo "container stop && rm success"
