# NPanel Migrator Server - 单二进制构建
#
# 构建链路：
#   1. pnpm 编译 ../Vue 前端 -> ../Vue/dist
#   2. 拷贝 ../Vue/dist -> ./internal/server/assets (供 go:embed)
#   3. wire 生成依赖注入代码
#   4. go build 产出单二进制 ./bin/npanel-migrator (内嵌前端 UI)
#
# 常用目标：
#   make build          # 全流程：前端+后端+单二进制
#   make build-frontend # 仅编译前端
#   make build-server   # 仅编译后端（假设 assets 已就绪）
#   make run            # 编译并本地运行
#   make clean          # 清理产物

# ===== 可配置变量 =====
GO           ?= go
PNPM         ?= pnpm
APP          := npanel-migrator
BIN_DIR      := bin
VUE_DIR      := ../Vue
ASSETS_DIR   := internal/server/assets
VERSION      ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS      := -X main.Version=$(VERSION)

.PHONY: all build build-frontend copy-frontend wire build-server run clean fmt vet help

# ===== 全流程构建 =====
all build: copy-frontend wire build-server

# ===== 1. 前端编译 =====
build-frontend:
	@echo ">> building frontend ($(VUE_DIR))"
	@cd $(VUE_DIR) && $(PNPM) install && $(PNPM) run build
	@echo ">> frontend built -> $(VUE_DIR)/dist"

# ===== 2. 拷贝前端产物到 embed 目录 =====
# 依赖 build-frontend，确保 dist 存在
copy-frontend: build-frontend
	@echo ">> copying frontend dist -> $(ASSETS_DIR)"
	@rm -rf $(ASSETS_DIR)
	@mkdir -p $(ASSETS_DIR)
	@cp -r $(VUE_DIR)/dist/. $(ASSETS_DIR)/
	@echo ">> assets ready for embed"

# ===== 3. wire 生成依赖注入 =====
wire:
	@echo ">> generating wire code"
	@cd cmd/$(APP) && wire

# ===== 4. 后端编译（assets 必须已就绪） =====
build-server:
	@echo ">> building server (version $(VERSION))"
	@$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(APP) ./cmd/$(APP)
	@echo ">> built $(BIN_DIR)/$(APP) (frontend embedded)"

# ===== 运行 =====
run: build
	@echo ">> running $(APP)"
	@./$(BIN_DIR)/$(APP) -conf ./configs

# ===== 清理 =====
clean:
	@rm -rf $(BIN_DIR) $(ASSETS_DIR)
	@echo ">> cleaned"

# ===== 辅助 =====
fmt:
	@$(GO) fmt ./...

vet:
	@$(GO) vet ./...

help:
	@echo "NPanel Migrator Server"
	@echo ""
	@echo "Targets:"
	@echo "  make build          - 全流程构建单二进制（前端+后端）"
	@echo "  make build-frontend - 仅编译前端"
	@echo "  make copy-frontend  - 编译前端并拷贝到 embed 目录"
	@echo "  make build-server   - 仅编译后端（需 assets 已就绪）"
	@echo "  make wire           - 生成 wire 依赖注入代码"
	@echo "  make run            - 构建并运行"
	@echo "  make clean          - 清理产物"
