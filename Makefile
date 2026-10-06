# mulix — build/test/lint/fmt/clean（对齐 design §13）
# 注意：本机（Windows 开发机）未安装 make，本地验证直接运行对应的 go 命令；
# CI（.github/workflows/ci.yml）在 linux 上执行 make。

GO      ?= go
BIN_DIR := bin

.PHONY: build test lint fmt clean cross

build:
	$(GO) build -o $(BIN_DIR)/mulix ./cmd/mulix

test:
	$(GO) test ./...

lint:
	golangci-lint run

fmt:
	$(GO) fmt ./...

clean:
	rm -rf $(BIN_DIR)

cross:
	GOOS=windows GOARCH=amd64 $(GO) build -o $(BIN_DIR)/mulix-windows-amd64.exe ./cmd/mulix
	GOOS=darwin GOARCH=amd64 $(GO) build -o $(BIN_DIR)/mulix-darwin-amd64 ./cmd/mulix
	GOOS=linux GOARCH=amd64 $(GO) build -o $(BIN_DIR)/mulix-linux-amd64 ./cmd/mulix
