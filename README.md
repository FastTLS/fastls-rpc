# fastls-rpc

FastTLS 的 RPC 服务集合，同一仓库提供 JSON-RPC 2.0 和 gRPC 两种接入方式。两种服务共享相同的浏览器/JA3/JA4R 指纹解析，并通过 `fastls` + `ftransport` 发起请求。

> 当前状态：`v0.1.0` 正式版。正式产物只在 GitHub Releases 发布；其他软件包、镜像和应用商店不在发布范围内。

[使用文档](https://fasttls.github.io/ecosystem/) · [下载发布包](https://github.com/FastTLS/fastls-rpc/releases)

下载对应系统/架构的 Release 产物后校验 SHA-256 和来源证明，解压运行；普通用户无需安装 Go。只有从源码构建才需要下述 Go 工具链。

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

GitHub Actions 覆盖 Windows、Linux、macOS 与两个 Go 版本，并执行依赖策略、单元/集成测试、竞态检测、覆盖率、静态检查、两种服务构建和 CLI 冒烟测试。`main` 分支生成临时开发包；`v*` 标签会生成同时包含两个二进制的六个平台正式包、SHA-256 校验和与 Sigstore/GitHub 来源证明，并只发布到 GitHub Releases。

本地集成测试共用环回目标与 CONNECT 代理夹具，覆盖请求体/请求头/Cookie、请求与响应 Trailer、最终协议/URL、压缩响应、禁用重定向、代理优先级、取消传播、JSON-RPC 的 CORS/请求大小边界，以及两个 RPC 适配层的字段映射；不访问公网。

默认测试还会分别构建并启动 JSON-RPC 与 gRPC CLI，通过真实 TCP 端口验证健康检查和 fetch。若同级目录存在三个迁移工作区，可运行 `GOWORK=off go test -count=3 -tags workspace_e2e ./integration/workspace`，构建 Fetch、JSON-RPC、gRPC、MITM 四个进程并验证三种入口经 MITM 访问同一个环回 HTTPS 目标。专用的 Workspace integration Action 支持固定另外两个仓库的候选 ref，并保存三仓精确提交号与测试日志。

## 文档索引

- [JSON-RPC 概览](./jsonrpc/README.md) / [完整 API](./jsonrpc/RPC_SERVER.md) / [客户端示例](./jsonrpc/example/README.md)
- [gRPC 概览](./grpc/README.md) / [完整 API](./grpc/GRPC_SERVER.md) / [客户端示例](./grpc/grpc_example/README.md)
- [开发与贡献](./DEVELOPMENT.md)
- [跨仓四进程联调](./integration/workspace/README.md)
- [开发产物及签发验证](./ARTIFACTS.md)

## 安全边界

两个服务都允许调用方指定目标地址和代理。JSON-RPC 请求体内置 4 MiB 上限，并使用符合凭据请求规则的显式 CORS 来源；gRPC 保留框架消息大小保护。不要把未加访问控制的实例暴露到不受信任网络；部署方仍应加入认证、更严格的请求限制、出口策略和审计。


## 不支持与部署责任

这是受控网络内的 HTTP 请求服务，不是完整浏览器、通用 VPN 或任意协议转发器。不能从核心的 HTTP/3 路径推断所有服务入口均支持 H3 指纹、代理组合或流式 RPC。默认示例不能替代认证、出口控制、租户隔离和流量限制。浏览器别名是服务适配层选择器，与 Go Provider 的规范 ID 不同；实际支持范围以该发布版本为准。

## 详细文档与问题反馈

- [使用文档](https://fasttls.github.io/ecosystem/)：安装、功能、参数与排错。
- [版本与发布说明](https://github.com/FastTLS/fastls-rpc/releases)：正式版本、开发版本和产物边界。
- [问题反馈](https://github.com/FastTLS/fastls-rpc/issues)：请提供版本、系统架构、脱敏后的最小示例与错误；不要提交凭据、Cookie、私钥或个人数据。

## 开源协议与第三方声明

FastTLS 自有代码采用 [Apache-2.0](LICENSE)，见 [NOTICE](NOTICE)。依赖及发布包内的第三方声明保留各自授权。

## 使用边界与免责声明

仅在拥有权限的系统、数据和网络上使用，并遵守适用法律、服务条款及隐私要求。软件按许可证约定以“现状”提供，不承诺可用性、特定用途适用性、第三方服务放行、匿名性或与真实浏览器完全一致。浏览器名称和版本用于描述配置，不表示获得相应厂商认证或背书。部署方负责访问控制、密钥和个人信息保护、请求限额及数据备份；担保排除与责任限制以随附许可证及适用法律为准。本说明不改变许可证授予的权利或新增许可限制。
