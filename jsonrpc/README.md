# Fastls JSON-RPC 2.0 服务

基于 JSON-RPC 2.0 协议的 RPC 服务器，提供 HTTP 请求功能，支持 TLS 指纹伪装。

服务收到 Ctrl+C、SIGINT 或 SIGTERM 后停止接受新连接，并给活动 HTTP 请求最多 5 秒完成；超过窗口后强制关闭。

## 目录结构

```
jsonrpc/
├── rpc_server.go          # JSON-RPC服务器主程序
├── RPC_SERVER.md          # JSON-RPC服务器API文档
├── README.md              # 本文件
└── example/               # 客户端示例
    ├── README.md          # 客户端使用说明
    ├── python_client.py   # Python客户端示例
    ├── go_client.go       # Golang客户端示例
    └── nodejs_client.js   # Node.js客户端示例
```

## 快速开始

### 编译

```bash
cd fastls-rpc
go build -o fastls-jsonrpc.exe ./jsonrpc
```

### 运行

```bash
./fastls-jsonrpc.exe
```

服务器默认运行在 `http://localhost:8801/rpc`

## 功能特性

- ✅ JSON-RPC 2.0 标准协议
- ✅ 多语言客户端支持（Python、Golang、Node.js）
- ✅ TLS 指纹伪装（JA3/JA4R）
- ✅ 浏览器预设支持（Chrome、Firefox、Edge等）
- ✅ 自动响应解压缩
- ✅ 代理支持
- ✅ CORS 支持

## 客户端示例

详细的客户端示例代码位于 `example/` 目录：

- **Python**: `example/python_client.py`
- **Golang**: `example/go_client.go`
- **Node.js**: `example/nodejs_client.js`

## 使用示例

### 启动服务器

```bash
cd fastls-rpc
go run ./jsonrpc
```

### 运行客户端示例

```bash
# Python
cd example
python python_client.py

# Golang
cd example
go run go_client.go

# Node.js
cd example
node nodejs_client.js
```

## 文档

- [JSON-RPC服务器API文档](./RPC_SERVER.md) - 详细的API说明和使用示例
- [客户端示例说明](./example/README.md) - 各语言客户端的使用方法

## 相关服务

- [gRPC服务](../grpc/) - 基于HTTP/2 + Protobuf的RPC服务
- [Fetch服务](https://github.com/FastTLS/fastls-fetch) - HTTP请求服务
- [MITM代理服务](https://github.com/FastTLS/fastls-mitm) - 中间人代理服务

