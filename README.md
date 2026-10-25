# fastls-rpc

FastTLS 的 RPC 服务集合，同一仓库提供 JSON-RPC 2.0 和 gRPC 两种接入方式。两种服务共享相同的浏览器/JA3/JA4R 指纹解析，并通过 `fastls` + `ftransport` 发起请求。

> 当前状态：开发阶段。仓库只生成带来源证明、保留 14 天的开发构建，不创建版本标签、GitHub Release 或正式发布包。

## 服务入口

| 服务 | 二进制 | 默认地址 | 接口 |
| --- | --- | --- | --- |
| JSON-RPC 2.0 | `fastls-jsonrpc` | `127.0.0.1:8801` | `POST /rpc`、`GET /health` |
| gRPC | `fastls-grpc` | `127.0.0.1:8802` | `FastlsService.Health`、`FastlsService.Fetch` |

JSON-RPC 适合无需生成客户端的 HTTP 集成；gRPC 适合需要 Protobuf 类型约束和标准 gRPC 工具链的场景。

## 环境要求

- Go 1.26 或更高版本；CI 同时覆盖 Go 1.26.10 和 1.27.3
- 使用仓库自己的 `go.mod`，建议设置 `GOWORK=off`
- 只有修改 `.proto` 时才需要 `protoc` 及 Go 插件；仓库已提交生成代码

## 从源码构建

```bash
git clone https://github.com/FastTLS/fastls-rpc.git
cd fastls-rpc
go mod download
go build -o fastls-jsonrpc ./jsonrpc
go build -o fastls-grpc ./grpc
```

Windows 为输出文件添加 `.exe`。

## 运行

```bash
./fastls-jsonrpc -addr 127.0.0.1:8801
./fastls-grpc -addr 127.0.0.1:8802

./fastls-jsonrpc -help
./fastls-grpc -version
```

JSON-RPC 快速请求：

```bash
curl -X POST http://127.0.0.1:8801/rpc \
  -H 'Content-Type: application/json' \
  -d '{
    "jsonrpc": "2.0",
    "method": "fetch",
    "params": {
      "url": "https://example.com/",
      "method": "GET",
      "browser": "chrome"
    },
    "id": 1
  }'
```

gRPC 的权威接口定义位于 [`grpc/proto/fastls.proto`](./grpc/proto/fastls.proto)。

## 指纹和传输

建议优先使用稳定别名 `chrome`、`edge`、`firefox`、`safari`、`opera` 或 `chromium`；需要固定实验条件时可以使用版本化选择器。显式 JA3/JA4R 高于 `browser`，未设置时使用默认浏览器配置。无效选择器会返回可诊断错误。

JSON-RPC 与 gRPC 共用同一请求执行层和字段优先级：`parent_proxy` > `proxy`，显式 HTTP/2 设置覆盖浏览器预设，调用取消会传递到下游连接。两种协议对无效指纹及 HTTP/2 设置分别返回 `-32602` 与 `INVALID_ARGUMENT`，避免同一请求在两个入口出现不同结果。

两个服务收到 Ctrl+C、SIGINT 或 SIGTERM 后停止接受新连接，并给活动 HTTP 请求或 RPC 最多 5 秒完成；超过窗口后会强制停止。

```text
fastls-rpc
  ├─ jsonrpc / grpc
  └─ fastls
      ├─ ftransport       HTTP/1.1、HTTP/2、HTTP/3 传输
      ├─ fastls/profile   指纹模型
      └─ imitate          浏览器预设
```

CI 会拒绝本地 `replace` 指令和任何重新引入 `github.com/FastTLS/fhttp` 的依赖图。

## 测试与开发构建

```bash
go mod verify
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

GitHub Actions 覆盖 Windows、Linux、macOS 与两个 Go 版本，并执行依赖策略、单元/集成测试、竞态检测、覆盖率、静态检查、两种服务构建和 CLI 冒烟测试。`main` 分支会生成同时包含两个二进制的六个平台开发包、SHA-256 校验和与 Sigstore/GitHub 来源证明，并在上传前完成验证。

本地集成测试共用环回目标与 CONNECT 代理夹具，覆盖请求体/请求头/Cookie、压缩响应、禁用重定向、代理优先级、取消传播，以及两个 RPC 适配层的字段映射；不访问公网。

## 文档索引

- [JSON-RPC 概览](./jsonrpc/README.md) / [完整 API](./jsonrpc/RPC_SERVER.md) / [客户端示例](./jsonrpc/example/README.md)
- [gRPC 概览](./grpc/README.md) / [完整 API](./grpc/GRPC_SERVER.md) / [客户端示例](./grpc/grpc_example/README.md)
- [开发与贡献](./DEVELOPMENT.md)
- [开发产物及签发验证](./ARTIFACTS.md)

## 安全边界

两个服务都允许调用方指定目标地址和代理。不要把未加访问控制的实例暴露到不受信任网络；部署方应加入认证、请求限制、出口策略和审计。开发构建不承诺稳定 API 或生产支持。
