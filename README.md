# Fastls RPC 服务

提供两种RPC实现方式，都支持HTTP请求功能和TLS指纹伪装。

## 服务类型

### JSON-RPC 2.0 服务

- **位置**: [jsonrpc/](./jsonrpc/)
- **端口**: 8801
- **协议**: HTTP + JSON
- **特点**: 简单易用，基于HTTP协议
- **文档**: [JSON-RPC服务文档](./jsonrpc/README.md)

### gRPC 服务

- **位置**: [grpc/](./grpc/)
- **端口**: 8802
- **协议**: HTTP/2 + Protobuf
- **特点**: 高性能，类型安全
- **文档**: [gRPC服务文档](./grpc/README.md)

## 快速对比

| 特性 | JSON-RPC 2.0 | gRPC |
|------|--------------|------|
| 协议 | HTTP + JSON | HTTP/2 + Protobuf |
| 性能 | 中等 | 高 |
| 类型安全 | 弱 | 强 |
| 流式传输 | 不支持 | 支持 |
| 复杂度 | 低 | 中等 |
| 端口 | 8801 | 8802 |
| 适用场景 | 简单RPC调用 | 高性能、类型安全要求高的场景 |

## 选择建议

### 使用 JSON-RPC 2.0 如果：
- 需要简单的RPC调用
- 希望使用HTTP协议，易于调试
- 不需要高性能要求
- 希望快速集成

### 使用 gRPC 如果：
- 需要高性能
- 需要类型安全
- 需要流式传输
- 需要跨语言支持

## 目录结构

```
rpc/
├── README.md              # 本文件
├── jsonrpc/               # JSON-RPC 2.0服务
│   ├── rpc_server.go
│   ├── RPC_SERVER.md
│   ├── README.md
│   └── example/
└── grpc/                  # gRPC服务
    ├── grpc_server.go
    ├── GRPC_SERVER.md
    ├── README.md
    ├── proto/
    └── grpc_example/
```

## 相关服务

- [Fetch服务](../fetch/) - HTTP请求服务
- [MITM代理服务](../mitm/) - 中间人代理服务
