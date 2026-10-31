# 请求服务地址策略

服务 v0.1.2 固定 Go 核心 `v0.1.3-0.20261030152832-0dc4eea0f32e`，普通独立构建可启用可信启动策略，无需本机 replace 或源码工作区。旧服务二进制不会因核心仓库更新自动获得地址修正。

## 核心能力与独立构建

服务检查编译后端的 `WithAddressPolicyJSON([]byte) (fastls.Fastls, error)` 能力；缺少能力时在监听前拒绝启动，不能静默禁用。设置 `GOWORK=off` 使用仓库锁定的公开模块。RPC 的 `policy_e2e` 覆盖 Fetch、JSON-RPC、gRPC 及 MITM 网关实际进程，旧核心拒绝另行验证。

## 启动选项

`-address-policy /trusted/path/policy.json` 只由运维设置，必须在监听端口前读取、校验并编译成功。省略或空路径保留原行为。文件最大1MiB，拒绝未知字段、重复键、null字段、错误类型、尾随JSON值和非法规则；规则键必须使用以下精确名称。没有热更新：启动后改写文件不改变已编译规则，更新策略需要重启服务。

```json
{
  "allow_schemes": ["https"],
  "allow_hosts": ["api.example.test"],
  "allow_ports": [443],
  "allow_cidrs": ["192.0.2.0/24", "2001:db8::/32"],
  "deny_cidrs": []
}
```

以上为文档用域名和地址，必须替换成实际获准目标。各非空允许维度同时满足，CIDR拒绝规则优先。允许列表为空/省略表示该维度不限制，**不会自动封禁私网或元数据服务**；端口须为1–65535整数。域名精确匹配，使用IDNA规范化，不支持通配符或隐式子域名。配置文件不能设置解析器、代理、日志接口或任意Go对象。

每个实际发送的请求和重定向跳均校验scheme/host/port。新连接解析出的全部DNS答案先通过CIDR检查，再仅向校验过的数值IP拨号；不会先允许部分答案或再次交由拨号器解析域名。Host覆盖须与URL的规范化主机和有效端口一致。HTTP1、HTTP2和指纹TLS继续保留原URL/Host/SNI；服务原有证书验证设置不因此改变。

启用策略后，proxy、parent_proxy、Fetch的X-Mitm-Proxy等配置都不能绕过约束；当前代理与HTTP3请求明确拒绝，而非转为未受控连接。请求中的address_policy、address_policy_file或同名对象不是有效的策略配置入口，不能清除客户端固定的策略；Protobuf没有对应策略输入字段。

## 拒绝结果

三个入口都使用已有结果封装，返回 `ok=false`、`error_code="address_denied"`，并在 `error` 提供简短Reason，例如host_not_allowed、port_not_allowed、scheme_not_allowed、cidr_denied、cidr_not_allowed、host_override、proxy_unsupported或http3_unsupported。Fetch仍使用HTTP200，JSON-RPC结果不是协议层error，gRPC不是框架状态错误。输入格式等原有协议错误通道不变。

策略错误消息及核心对应拒绝日志不回显外层出站URL的凭据或查询字符串；其他现有日志与错误不保证脱敏，这不是完整审计接口。拒绝后释放并发名额，健康检查仍可使用。禁止重定向时返回上游3xx，不访问Location；HTTP4xx/5xx响应本身也不等于策略拒绝。

## 开发验证与边界

服务 v0.1.2 的公开模块图支持独立策略构建。普通单测检查默认兼容；RPC 的正式 `policy_e2e` 与六平台下载消费检查覆盖真实允许/拒绝、H1/H2、重定向、代理拒绝、配置快照和名额归还。旧核心拒绝使用临时独立模块缓存验证，不修改生产依赖或把旧校验失败静默跳过。显式开发工作区仍可测试未发布核心，但其结论必须与正式字节分开记录。

请求大小、TCP 接入名额、解码前请求名额及默认关闭的有限审计已交付，见 [接入限制](INGRESS.md) 和 [审计](AUDIT.md)。MITM v0.1.2 已包含独立策略、请求/隧道并发、寿命和双向 TLS 字节预算。完整流量记录、全实例速率控制仍不在范围内；SDK 的完整 Spec 是独立接口，不能推导为服务请求 DTO 已扩展。目标服务转发、路由/NAT与业务权限仍需应用和网络层约束。

## 未指定目标与升级

`0.0.0.0`、`::` 和 IPv4 映射零地址无条件按 `invalid_ip` 拒绝，即使配置了覆盖全部地址的允许 CIDR；它们不能被操作系统解释成回环目标来绕过拒绝规则。普通请求、重定向和实际新连接均受固定策略约束。空允许列表仍不等于自动禁止私网。服务未新增 H3 协议选择、UDP 代理或 Alt-Svc 支持；核心已有能力不能推导为服务入口支持。
