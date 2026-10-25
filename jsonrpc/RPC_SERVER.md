# Fastls RPC 服务器

基于 JSON-RPC 2.0 协议的 RPC 服务器，提供 HTTP 请求功能，支持 TLS 指纹伪装。

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

## API 文档

### 端点

- **RPC端点**: `POST /rpc`
- **健康检查**: `GET /health`

### JSON-RPC 2.0 协议

所有请求必须遵循 JSON-RPC 2.0 规范：

```json
{
  "jsonrpc": "2.0",
  "method": "方法名",
  "params": {},
  "id": 1
}
```

响应格式：

```json
{
  "jsonrpc": "2.0",
  "result": {},
  "id": 1
}
```

或错误响应：

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

## 方法

### health

健康检查方法，用于验证服务器是否正常运行。

**请求:**
```json
{
  "jsonrpc": "2.0",
  "method": "health",
  "params": {},
  "id": 1
}
```

**响应:**
```json
{
  "jsonrpc": "2.0",
  "result": {
    "status": "ok"
  },
  "id": 1
}
```

### fetch

发送 HTTP 请求，支持 TLS 指纹伪装。

**请求参数:**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| url | string | 是 | 请求URL |
| method | string | 否 | HTTP方法，默认 "GET" |
| headers | object | 否 | 请求头 |
| body | string | 否 | 请求体 |
| proxy | string | 否 | 代理地址 |
| parent_proxy | string | 否 | 上级代理地址；设置后覆盖 `proxy` |
| timeout | int | 否 | 超时时间（秒），默认 30 |
| disableRedirect | bool | 否 | 是否禁用重定向，默认 false |
| userAgent | string | 否 | User-Agent |
| fingerprint | object | 否 | 指纹配置，格式：`{"type": "ja3", "value": "..."}` 或 `{"type": "ja4r", "value": "..."}` |
| browser | string | 否 | 浏览器类型 |
| cookies | array | 否 | Cookie列表 |
| http2_settings_string | string | 否 | HTTP/2 SETTINGS、窗口及伪头顺序字符串；设置后覆盖浏览器预设值 |

请求契约与 gRPC 服务一致：`parent_proxy` > `proxy`，显式 `userAgent` 覆盖浏览器生成值（`headers.User-Agent` 仍最高），显式 `http2_settings_string` 覆盖浏览器预设。字段不完整、类型不支持或 HTTP/2 设置无法解析时返回 `-32602 Invalid params`。客户端取消 JSON-RPC HTTP 请求时，下游请求同步取消。

**支持的浏览器类型:**

- 滚动别名：`chrome`、`edge`、`firefox`、`chromium`、`safari`、`opera`
- 版本选择器：`chrome150`…`chrome160`、`edge150`…`edge160`、`firefox151`…`firefox158`
- 兼容保留：`chrome120`、`chrome142`、`firefox144`、`firefox147`、`firefox148`

**请求示例:**
```json
{
  "jsonrpc": "2.0",
  "method": "fetch",
  "params": {
    "url": "https://tls.peet.ws/api/all",
    "method": "GET",
    "browser": "chrome142"
  },
  "id": 1
}
```

**响应示例:**
```json
{
  "jsonrpc": "2.0",
  "result": {
    "ok": true,
    "status": 200,
    "headers": {
      "Content-Type": "application/json",
      "Content-Length": "1234"
    },
    "body": "响应体内容"
  },
  "id": 1
}
```

## 错误代码

| 代码 | 说明 |
|------|------|
| -32700 | Parse error - 解析错误 |
| -32600 | Invalid Request - 无效请求 |
| -32601 | Method not found - 方法不存在 |
| -32602 | Invalid params - 无效参数 |
| -32603 | Internal error - 内部错误 |

## 使用示例

### cURL

```bash
curl -X POST http://localhost:8801/rpc \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "method": "fetch",
    "params": {
      "url": "https://tls.peet.ws/api/all",
      "browser": "chrome142"
    },
    "id": 1
  }'
```

### Python

```python
import requests

payload = {
    "jsonrpc": "2.0",
    "method": "fetch",
    "params": {
        "url": "https://tls.peet.ws/api/all",
        "browser": "chrome142"
    },
    "id": 1
}

response = requests.post("http://localhost:8801/rpc", json=payload)
print(response.json())
```

### Node.js

```javascript
const http = require('http');

const data = JSON.stringify({
    jsonrpc: '2.0',
    method: 'fetch',
    params: {
        url: 'https://tls.peet.ws/api/all',
        browser: 'chrome142'
    },
    id: 1
});

const options = {
    hostname: 'localhost',
    port: 8801,
    path: '/rpc',
    method: 'POST',
    headers: {
        'Content-Type': 'application/json',
        'Content-Length': data.length
    }
};

const req = http.request(options, (res) => {
    let data = '';
    res.on('data', (chunk) => { data += chunk; });
    res.on('end', () => { console.log(JSON.parse(data)); });
});

req.write(data);
req.end();
```

## 指纹设置优先级

1. **自定义指纹** (`fingerprint`): 如果指定了 `fingerprint` 对象（JA3或JA4R），优先使用
2. **浏览器类型** (`browser`): 如果没有指定指纹，根据 `browser` 参数设置对应的浏览器指纹
3. **默认指纹**: 如果既没有指定指纹也没有指定浏览器，使用 Firefox 指纹

## 注意事项

1. 服务器默认监听 `:8801` 端口，可以通过修改代码中的 `port` 变量来更改
2. 所有请求都支持 CORS，允许跨域访问
3. 响应体会自动解压缩（支持 gzip、deflate、br、zstd）
4. 超时时间默认为 30 秒，可以通过 `timeout` 参数自定义
5. 支持 HTTP 和 HTTPS 请求
6. 支持代理设置（HTTP、HTTPS、SOCKS5）

## 客户端示例

详细的客户端示例代码请参考 `example/` 目录：

- `python_client.py` - Python 客户端示例
- `go_client.go` - Golang 客户端示例
- `nodejs_client.js` - Node.js 客户端示例

## 性能优化

- 使用全局 Fastls 客户端实例，支持高并发
- 自动设置最大 CPU 核心数
- 响应体自动解压缩
- 支持连接复用

