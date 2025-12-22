# Fastls gRPC 客户端示例

本目录包含使用 Fastls gRPC 服务的客户端示例，支持 Python、Golang 和 Node.js。

## 生成代码

### Go

Go代码会在编译时自动生成，无需手动执行。

### Python

```bash
# 安装依赖
pip install grpcio grpcio-tools

# 生成Python代码
cd services/fastls-rpc/grpc/grpc_example
python -m grpc_tools.protoc -I../proto --python_out=. --grpc_python_out=. ../proto/fastls.proto
```

### Node.js

Node.js使用动态加载proto文件，无需生成代码。

## 启动gRPC服务器

```bash
cd services/fastls-rpc/grpc
# 先生成protobuf代码
protoc --go_out=. --go_opt=paths=source_relative \
       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
       proto/fastls.proto
# 运行服务器
go run grpc_server.go
```

服务器默认运行在 `localhost:8802`

## Python 客户端

### 安装依赖

```bash
pip install grpcio
```

### 生成Python代码

```bash
cd services/fastls-rpc
python -m grpc_tools.protoc -I./proto --python_out=./grpc_example --grpc_python_out=./grpc_example ./proto/fastls.proto
```

### 运行示例

```bash
cd grpc_example
python python_client.py
```

### 使用示例

```python
import grpc
from fastls_pb2 import FetchRequest
from fastls_pb2_grpc import FastlsServiceStub

# 连接到服务器
channel = grpc.insecure_channel('localhost:8802')
stub = FastlsServiceStub(channel)

# 发送请求
response = stub.Fetch(FetchRequest(
    url="https://tls.peet.ws/api/all",
    browser="chrome142"
))

print(response.body)
```

## Golang 客户端

### 运行示例

```bash
cd grpc_example
go run go_client.go
```

### 使用示例

```go
package main

import (
    "context"
    "fmt"
    pb "github.com/FastTLS/fastls-rpc/grpc/proto"
    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"
)

func main() {
    conn, _ := grpc.Dial("localhost:8802", grpc.WithTransportCredentials(insecure.NewCredentials()))
    defer conn.Close()
    
    client := pb.NewFastlsServiceClient(conn)
    
    resp, _ := client.Fetch(context.Background(), &pb.FetchRequest{
        Url:     "https://tls.peet.ws/api/all",
        Browser: "chrome142",
    })
    
    fmt.Println(resp.Body)
}
```

## Node.js 客户端

### 安装依赖

```bash
npm install @grpc/grpc-js @grpc/proto-loader
```

### 运行示例

```bash
cd grpc_example
node nodejs_client.js
```

### 使用示例

```javascript
const grpc = require('@grpc/grpc-js');
const protoLoader = require('@grpc/proto-loader');

const packageDefinition = protoLoader.loadSync('../proto/fastls.proto', {
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

## gRPC方法

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

**请求:**
```protobuf
message FetchRequest {
  string url = 1;
  string method = 2;
  map<string, string> headers = 3;
  string body = 4;
  string proxy = 5;
  int32 timeout = 6;
  bool disable_redirect = 7;
  string user_agent = 8;
  Fingerprint fingerprint = 9;
  string browser = 10;
  repeated Cookie cookies = 11;
}
```

**响应:**
```protobuf
message FetchResponse {
  bool ok = 1;
  int32 status = 2;
  map<string, string> headers = 3;
  string body = 4;
  string error = 5;
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

## 与JSON-RPC对比

| 特性 | JSON-RPC | gRPC |
|------|----------|------|
| 协议 | HTTP + JSON | HTTP/2 + Protobuf |
| 性能 | 中等 | 高 |
| 类型安全 | 弱 | 强 |
| 流式传输 | 不支持 | 支持 |
| 跨语言 | 是 | 是 |
| 复杂度 | 低 | 中等 |

## 注意事项

1. gRPC使用HTTP/2协议，需要支持HTTP/2的客户端
2. 默认使用不安全的连接（insecure），生产环境应使用TLS
3. Python需要先生成protobuf代码
4. Node.js使用动态加载，无需生成代码

