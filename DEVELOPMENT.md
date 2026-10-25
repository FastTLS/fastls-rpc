# 开发说明

## 开发基线

仓库是独立 Go module。设置 `GOWORK=off`，不得依赖父目录工作区、未提交的本地模块或 `replace` 指令。

```bash
go mod download
go mod verify
go test ./...
go vet ./...
go build ./...
```

最低基线为 Go 1.26；CI 的兼容矩阵为 Go 1.26.10/1.27.3 × Ubuntu 24.04/Windows 2022/macOS 15。

## 提交前检查

```bash
go mod tidy
git diff --exit-code -- go.mod go.sum
go test -race ./...
go test -covermode=atomic -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
go build -o build/fastls-jsonrpc ./jsonrpc
go build -o build/fastls-grpc ./grpc
./build/fastls-jsonrpc -version
./build/fastls-grpc -version
```

涉及请求传输的测试使用本地 HTTP/TLS 测试服务器。共享的指纹选择逻辑位于 `internal/fingerprint`，修复应同时验证 JSON-RPC 和 gRPC，不要在两个入口复制不同规则。

服务生命周期逻辑位于 `internal/serverlifecycle`。相关变更必须通过环回监听器验证 context 取消、停止接受新连接，并确认活动 HTTP/RPC 在超时窗口内完成后才退出。

JSON-RPC HTTP 入口变更必须覆盖 CORS 凭据语义和 4 MiB 请求体边界；超限响应同时验证 HTTP `413` 与 JSON-RPC `-32600`。

`jsonrpc/binary_e2e_test.go` 与 `grpc/binary_e2e_test.go` 会分别构建并启动当前 CLI，通过真实 TCP 端口验证健康检查和 fetch 契约。进程管理统一使用 `internal/testprocess`。

当同级目录存在 `fastls-fetch-ftransport` 与 `fastls-mitm-ftransport` 时，可运行 `GOWORK=off go test -count=3 -tags workspace_e2e ./integration/workspace`。该测试先校验三仓共享依赖版本一致且没有 `replace`，再构建四个真实服务进程，并验证 Fetch、JSON-RPC、gRPC 都能经 MITM 访问同一个环回 HTTPS 目标；负向用例验证无效配置、上级代理拒绝 CONNECT 时禁止静默直连，以及失败后的服务健康状态。普通单仓测试矩阵不启用此标签。组合测试使用 HTTPS，因为 `ftransport` 的上游代理路径使用 CONNECT，而 MITM 按 TLS 隧道处理 CONNECT 目标。

`.github/workflows/workspace-integration.yml` 是跨仓协调门禁：RPC 拉取请求和主分支运行使用另外两个仓库的 `main`，手动运行可以固定 Fetch、MITM 的分支、标签或提交，RPC ref 由工作流启动位置决定。结果 artifact 必须包含三仓精确提交号、模块依赖清单和联调日志；候选验证不得省略 ref 记录。完整说明见 `integration/workspace/README.md`。

## Protobuf 变更

`grpc/proto/fastls.proto` 是接口源。当前 Go 生成基线为 protoc 33.2、`protoc-gen-go` v1.36.11 和 `protoc-gen-go-grpc` v1.6.0；Python 生成基线为 `grpcio-tools` 1.76.0（Protobuf Python 6.31.1）。只有 `.proto` 变化或修复生成文件时才重新生成：

```bash
protoc --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  grpc/proto/fastls.proto

python -m grpc_tools.protoc -I grpc/proto \
  --python_out=grpc/grpc_example \
  --grpc_python_out=grpc/grpc_example \
  grpc/proto/fastls.proto
```

提交时同时包含源文件、Go/Python 生成文件和兼容性说明。不要手工修改生成文件；Go 生成器版本不得高于运行时依赖 `google.golang.org/protobuf`。

## CI 门禁

`.github/workflows/ci.yml` 执行：

1. module 校验、`go mod tidy` 零差异、禁止 `replace`、禁止 `fhttp`。
2. 三平台、两个 Go 版本的测试、`go vet` 和全包构建。
3. Linux 竞态检测、覆盖率、两个 CLI 的构建及 `-version` 冒烟测试。

所有第三方 Actions 固定到完整提交 SHA；Pull Request 只获得读取权限。

## 开发产物

合入 `main` 或手动运行 `Development artifacts` 后，工作流会再次验证源码，再构建、计算校验和、签发来源证明并自验证。它不创建标签或 Release。详见 [ARTIFACTS.md](./ARTIFACTS.md)。

## 文档同步

修改请求字段时，同时更新 `.proto`（适用时）、JSON-RPC 文档、gRPC 文档和至少一个客户端示例。修改指纹选择范围时同步两个服务的帮助输出和文档。
