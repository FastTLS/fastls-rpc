# Fastls RPC 客户端示例

本目录包含使用 Fastls RPC 服务的客户端示例，支持 Python、Golang 和 Node.js。

## 启动RPC服务器

```bash
cd services/fastls-rpc
go run rpc_server.go
```

服务器默认运行在 `http://localhost:8801/rpc`

## Python 客户端

### 安装依赖

```bash
pip install requests
```

### 运行示例

```bash
python python_client.py
```

### 使用示例

```python
from python_client import FastlsRPCClient

# 创建客户端
client = FastlsRPCClient("http://localhost:8801/rpc")

# 健康检查
health = client.health()
print(health)

# 发送请求
result = client.fetch(
    url="https://tls.peet.ws/api/all",
    browser="chrome142"
)
print(result)
```

## Golang 客户端

### 运行示例

```bash
go run go_client.go
```

### 使用示例

```go
package main

import (
    "fmt"
    "github.com/FastTLS/fastls-rpc/jsonrpc/example"
)

func main() {
    // 创建客户端
    client := example.NewFastlsRPCClient("http://localhost:8801/rpc")
    
    // 健康检查
    health, _ := client.Health()
    fmt.Println(health)
    
    // 发送请求
    result, _ := client.Fetch(example.FetchParams{
        URL:     "https://tls.peet.ws/api/all",
        Browser: "chrome142",
    })
    fmt.Println(result)
}
```

## Node.js 客户端

### 运行示例

```bash
node nodejs_client.js
```

### 使用示例

```javascript
const FastlsRPCClient = require('./nodejs_client.js');

// 创建客户端
const client = new FastlsRPCClient('http://localhost:8801/rpc');

// 健康检查
client.health().then(health => {
    console.log(health);
});

// 发送请求
client.fetch({
    url: 'https://tls.peet.ws/api/all',
    browser: 'chrome142'
}).then(result => {
    console.log(result);
});
```

## RPC方法

### health

健康检查方法。

**参数:** 无

**返回:**
```json
{
  "status": "ok"
}
```

### fetch

发送HTTP请求。

**参数:**
```json
{
  "url": "https://example.com",
  "method": "GET",
  "headers": {},
  "body": "",
  "proxy": "",
  "timeout": 30,
  "disableRedirect": false,
  "userAgent": "",
  "fingerprint": {
    "type": "ja3",
    "value": "..."
  },
  "browser": "chrome142",
  "cookies": []
}
```

**返回:**
```json
{
  "ok": true,
  "status": 200,
  "headers": {},
  "body": "...",
  "error": ""
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

## 错误处理

RPC响应中的错误格式：

```json
{
  "jsonrpc": "2.0",
  "error": {
    "code": -32602,
    "message": "Invalid params",
    "data": "错误详情"
  },
  "id": 1
}
```

常见错误代码：
- `-32700` - Parse error（解析错误）
- `-32600` - Invalid Request（无效请求）
- `-32601` - Method not found（方法不存在）
- `-32602` - Invalid params（无效参数）
- `-32603` - Internal error（内部错误）

