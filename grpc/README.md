# Fastls gRPC 服务

基于 gRPC 协议的 RPC 服务器，提供 HTTP 请求功能，支持 TLS 指纹伪装。

## 目录结构

```
grpc/
├── grpc_server.go          # gRPC服务器主程序
├── GRPC_SERVER.md          # gRPC服务器API文档
├── README.md               # 本文件
├── proto/                  # Protobuf定义文件
│   └── fastls.proto
└── grpc_example/           # 客户端示例
    ├── README.md           # 客户端使用说明
    ├── python_client.py    # Python客户端示例
    ├── go_client.go         # Golang客户端示例
    ├── nodejs_client.js     # Node.js客户端示例
    ├── generate_proto.sh    # 代码生成脚本（Linux/Mac）
    └── generate_proto.bat   # 代码生成脚本（Windows）
```

## 快速开始

### 安装依赖

```bash
go get google.golang.org/grpc
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

### 生成代码

首先需要安装protoc工具（参考 https://grpc.io/docs/protoc-installation/）

```bash
cd services/fastls-rpc/grpc
protoc --go_out=. --go_opt=paths=source_relative \
       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
       proto/fastls.proto
```

### 编译

```bash
go build -o grpc_server.exe grpc_server.go
```

### 运行

```bash
./grpc_server.exe
```

服务器默认运行在 `localhost:8802`

## 功能特性

- ✅ gRPC 标准协议（HTTP/2 + Protobuf）
- ✅ 高性能二进制序列化
- ✅ 类型安全
- ✅ 多语言客户端支持（Python、Golang、Node.js）
- ✅ TLS 指纹伪装（JA3/JA4R）
- ✅ 浏览器预设支持（Chrome、Firefox、Edge等）
- ✅ 自动响应解压缩
- ✅ 代理支持

## 客户端示例

详细的客户端示例代码位于 `grpc_example/` 目录：

- **Python**: `grpc_example/python_client.py`
- **Golang**: `grpc_example/go_client.go`
- **Node.js**: `grpc_example/nodejs_client.js`

## 使用示例

### 启动服务器

```bash
cd services/fastls-rpc/grpc
# 先生成protobuf代码
protoc --go_out=. --go_opt=paths=source_relative \
       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
       proto/fastls.proto
# 运行服务器
go run grpc_server.go
```

### 运行客户端示例

```bash
# Python（需要先生成protobuf代码）
cd grpc_example
python -m grpc_tools.protoc -I../proto --python_out=. --grpc_python_out=. ../proto/fastls.proto
python python_client.py

# Golang
cd grpc_example
go run go_client.go

# Node.js
cd grpc_example
npm install @grpc/grpc-js @grpc/proto-loader
node nodejs_client.js
```

## 文档

- [gRPC服务器API文档](./GRPC_SERVER.md) - 详细的API说明和使用示例
- [客户端示例说明](./grpc_example/README.md) - 各语言客户端的使用方法

## 与JSON-RPC对比

| 特性 | JSON-RPC 2.0 | gRPC |
|------|--------------|------|
| 协议 | HTTP + JSON | HTTP/2 + Protobuf |
| 性能 | 中等 | 高 |
| 类型安全 | 弱 | 强 |
| 流式传输 | 不支持 | 支持 |
| 复杂度 | 低 | 中等 |
| 端口 | 8801 | 8802 |

## 相关服务

- [JSON-RPC服务](../jsonrpc/) - 基于HTTP + JSON的RPC服务
- [Fetch服务](../../fetch/) - HTTP请求服务
- [MITM代理服务](../../mitm/) - 中间人代理服务

