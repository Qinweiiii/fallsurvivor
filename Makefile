.DEFAULT_GOAL := help
SHELL := /bin/bash

BACKEND_DIR := backend
FRONTEND_DIR := frontend
WORKER_DIR := worker-browser

# 加载根目录 .env 中的环境变量（后端从环境变量读取配置）
# 优先级：已在 shell 中 export 的变量 > .env 中的值。
# 这样你可以在终端 `export DEEPSEEK_API_KEY=...` 后再 make server，
# 而无需把密钥写进 .env 文件；.env 只负责填补 shell 里缺失的项。
# 用法：$(call run_backend,./cmd/server)
define run_backend
	set -a; \
	if [ -f .env ]; then \
		while IFS= read -r line || [ -n "$$line" ]; do \
			case "$$line" in \
				''|\#*) ;; \
				*=*) k="$${line%%=*}"; v="$${line#*=}"; \
				     [ -z "$${!k+x}" ] && export "$$k=$$v" ;; \
			esac; \
		done < .env; \
	fi; \
	set +a; \
	cd $(BACKEND_DIR) && go run $(1)
endef

.PHONY: help
help: ## 显示所有可用命令
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

# ---------------- 环境 ----------------
.PHONY: env
env: ## 从模板创建 .env（不覆盖已有文件）
	@[ -f .env ] || (cp .env.example .env && echo "已创建 .env，请填写 DATABASE_URL / REDIS_URL / API Key")

.PHONY: up
up: ## 启动 postgres + redis + backend + frontend
	docker compose up -d --build

.PHONY: infra
infra: ## 只启动 postgres + redis（优先 Docker，无 Docker 时用 brew）
	@if command -v docker >/dev/null 2>&1; then \
		docker compose up -d postgres redis; \
	elif command -v brew >/dev/null 2>&1; then \
		echo "未检测到 Docker，改用 Homebrew 服务"; \
		brew services start postgresql@16 2>/dev/null || brew services start postgresql; \
		brew services start redis; \
	else \
		echo "既没有 Docker 也没有 Homebrew，请手动安装 PostgreSQL 与 Redis"; exit 1; \
	fi

.PHONY: infra-stop
infra-stop: ## 停止本机的 postgres + redis（brew 路径）
	brew services stop postgresql@16 2>/dev/null || true
	brew services stop redis

.PHONY: down
down: ## 停止所有容器
	docker compose down

.PHONY: logs
logs: ## 查看容器日志
	docker compose logs -f --tail=100

# ---------------- 后端 ----------------
.PHONY: deps
deps: ## 拉取后端依赖
	cd $(BACKEND_DIR) && go mod tidy

.PHONY: migrate
migrate: ## 执行数据库迁移
	$(call run_backend,./cmd/migrate up)

.PHONY: migrate-down
migrate-down: ## 回滚最后一个迁移
	$(call run_backend,./cmd/migrate down)

.PHONY: seed
seed: ## 写入默认用户与求职画像（幂等）
	$(call run_backend,./cmd/migrate seed)

.PHONY: server
server: ## 本机运行 API 服务
	$(call run_backend,./cmd/server)

.PHONY: worker
worker: ## 本机运行异步任务消费者
	$(call run_backend,./cmd/worker)

.PHONY: build
build: ## 编译后端二进制
	cd $(BACKEND_DIR) && go build -o bin/server ./cmd/server && go build -o bin/worker ./cmd/worker

.PHONY: test
test: ## 运行后端单测
	cd $(BACKEND_DIR) && go test ./... -count=1

.PHONY: vet
vet: ## go vet 静态检查
	cd $(BACKEND_DIR) && go vet ./...

# ---------------- 前端 ----------------
.PHONY: fe-install
fe-install: ## 安装前端依赖
	cd $(FRONTEND_DIR) && npm install

.PHONY: fe
fe: ## 启动前端开发服务器
	cd $(FRONTEND_DIR) && npm run dev

# ---------------- Playwright Worker ----------------
.PHONY: bw-install
bw-install: ## 安装 Worker 依赖与 Chromium
	cd $(WORKER_DIR) && npm install && npx playwright install chromium

.PHONY: bw
bw: ## 启动 Playwright Worker（宿主机运行，自动加载 .env）
	@set -a; \
	if [ -f .env ]; then \
		while IFS= read -r line || [ -n "$$line" ]; do \
			case "$$line" in \
				''|\#*) ;; \
				*=*) k="$${line%%=*}"; v="$${line#*=}"; \
				     [ -z "$${!k+x}" ] && export "$$k=$$v" ;; \
			esac; \
		done < .env; \
	fi; \
	set +a; \
	echo "启动 Worker（token: $${BROWSER_WORKER_TOKEN:+已配置}$${BROWSER_WORKER_TOKEN:-未配置，仅限本机开发}）"; \
	cd $(WORKER_DIR) && npm run dev

# ---------------- 一键 ----------------
.PHONY: bootstrap
bootstrap: env deps fe-install bw-install ## 首次初始化全部依赖
	@echo "初始化完成。接着执行： make infra && make migrate && make seed"
