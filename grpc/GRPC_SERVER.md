# Fastls gRPC 服务器

基于 gRPC 协议的 RPC 服务器，提供 HTTP 请求功能，支持 TLS 指纹伪装。

## 快速开始

### 安装依赖

```bash
go get google.golang.org/grpc
go get google.golang.org/protobuf/cmd/protoc-gen-go
go get google.golang.org/grpc/cmd/protoc-gen-go-grpc
```

### 生成代码

```bash
cd services/fastls-rpc/grpc
protoc --go_out=. --go_opt=paths=source_relative \
       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
       proto/fastls.proto
```

### 编译

```bash
cd services/fastls-rpc/grpc
go build -o grpc_server.exe grpc_server.go
```

### 运行

```bash
./grpc_server.exe
```

服务器默认运行在 `localhost:8802`

## API 文档

### 服务定义

```protobuf
service FastlsService {
  rpc Health(HealthRequest) returns (HealthResponse);
  rpc Fetch(FetchRequest) returns (FetchResponse);
}
```

### Health

健康检查方法。

**请求:**
```protobuf
message HealthRequest {
}
```

**响应:**
```protobuf
message HealthResponse {
  string status = 1;
}
```

### Fetch

发送HTTP请求。

**请求参数:**

| 字段 | 类型 | 说明 |
|------|------|------|
| url | string | 请求URL（必填） |
| method | string | HTTP方法，默认 "GET" |
| headers | map<string, string> | 请求头 |
| body | string | 请求体 |
| proxy | string | 代理地址 |
| timeout | int32 | 超时时间（秒），默认 30 |
| disable_redirect | bool | 是否禁用重定向 |
| user_agent | string | User-Agent |
| fingerprint | Fingerprint | 指纹配置 |
| browser | string | 浏览器类型 |
| cookies | repeated Cookie | Cookie列表 |

**响应:**

```protobuf
message FetchResponse {
  bool ok = 1;                        // 是否成功
  int32 status = 2;                  // HTTP状态码
  map<string, string> headers = 3;   // 响应头
  string body = 4;                    // 响应体
  string error = 5;                   // 错误信息
}
```

## 支持的浏览器类型

- `chrome` - Chrome浏览器
- `chrome120` - Chrome 120版本
- `chrome142` - Chrome 142版本
- `chromium` - Chromium浏览器
- `edge` - Microsoft Edge浏览器
- `firefox` - Firefox浏览器
- `safari` - Safari浏览器
- `opera` - Opera浏览器

## 指纹类型

- `ja3` - JA3指纹
- `ja4r` - JA4R指纹（完整版本）

## 使用示例

### Python

```python
import grpc
from fastls_pb2 import FetchRequest
from fastls_pb2_grpc import FastlsServiceStub

channel = grpc.insecure_channel('localhost:8802')
stub = FastlsServiceStub(channel)

response = stub.Fetch(FetchRequest(
    url="https://tls.peet.ws/api/all",
    browser="chrome142"
))

print(response.body)
```

### Golang

```go
conn, _ := grpc.Dial("localhost:8802", grpc.WithInsecure())
defer conn.Close()

client := pb.NewFastlsServiceClient(conn)

resp, _ := client.Fetch(context.Background(), &pb.FetchRequest{
    Url:     "https://tls.peet.ws/api/all",
    Browser: "chrome142",
})

fmt.Println(resp.Body)
```

### Node.js

```javascript
const grpc = require('@grpc/grpc-js');
const protoLoader = require('@grpc/proto-loader');

const packageDefinition = protoLoader.loadSync('proto/fastls.proto', {
    keepCase: true,
    longs: String,
    enums: String,
    defaults: true,
    oneofs: true
});

const fastlsProto = grpc.loadPackageDefinition(packageDefinition).fastls;
const client = new fastlsProto.FastlsService(
    'localhost:8802',
    grpc.credentials.createInsecure()
);

client.Fetch({
    url: 'https://tls.peet.ws/api/all',
    browser: 'chrome142'
}, (error, response) => {
    if (!error) {
        console.log(response.body);
    }
});
```

## 与JSON-RPC对比

| 特性 | JSON-RPC | gRPC |
|------|----------|------|
| 协议 | HTTP + JSON | HTTP/2 + Protobuf |
| 性能 | 中等 | 高 |
| 类型安全 | 弱 | 强 |
| 流式传输 | 不支持 | 支持 |
| 跨语言 | 是 | 是 |
| 复杂度 | 低 | 中等 |
| 端口 | 8801 | 8802 |

## 性能优势

1. **二进制协议**: Protobuf比JSON更紧凑，序列化/反序列化更快
2. **HTTP/2**: 支持多路复用，减少连接开销
3. **类型安全**: 编译时类型检查，减少运行时错误
4. **流式传输**: 支持服务器流、客户端流和双向流

## 安全配置

### 使用TLS

```go
creds, err := credentials.NewServerTLSFromFile("server.crt", "server.key")
if err != nil {
    log.Fatalf("加载TLS证书失败: %v", err)
}

s := grpc.NewServer(grpc.Creds(creds))
```

### 客户端TLS

```go
creds, err := credentials.NewClientTLSFromFile("ca.crt", "")
if err != nil {
    log.Fatalf("加载CA证书失败: %v", err)
}

conn, err := grpc.Dial("localhost:8802", grpc.WithTransportCredentials(creds))
```

## 客户端示例

详细的客户端示例代码位于 `grpc/grpc_example/` 目录：

- **Python**: `grpc_example/python_client.py`
- **Golang**: `grpc_example/go_client.go`
- **Node.js**: `grpc_example/nodejs_client.js`

## 相关文档

- [gRPC客户端示例说明](./grpc_example/README.md) - 各语言客户端的使用方法
- [JSON-RPC服务器文档](./RPC_SERVER.md) - JSON-RPC 2.0 服务器文档

