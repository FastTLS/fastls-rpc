# 请求服务地址策略（开发源码）

本页描述P03-B2新增的Fetch、JSON-RPC与gRPC启动配置，尚未发布。后续P03-B3已在MITM开发源码增加独立安全策略模式；隧道配额和FFI策略输入仍未实现。

## 核心能力与独立构建

当前公开依赖仍是fastls v0.1.1，没有地址策略能力。默认源码构建、测试及不指定策略的服务继续使用正式依赖，不需要本机replace。**这类二进制指定策略后会拒绝启动，不能使用本页能力。**

服务通过编译进二进制的核心是否实现 `WithAddressPolicyJSON([]byte) (fastls.Fastls, error)` 检查能力。缺少能力时返回“policy-capable fastls core; refusing to start”，绝不静默禁用策略。当前可在显式绑定新核心源码的开发工作区中使用；需要普通独立构建也能启用时，应先正式发布核心并同步更新公开依赖，不能虚构版本或修改go.sum来绕过此条件。启动检查不是动态加载库的机制，替换配置文件不能给旧核心添加能力。

本地开发验收使用工作区提供的 `verify-p03-services.work`（现已包含核心、profile、ftransport、Fetch、RPC、MITM六个模块），并显式设置绝对 `GOWORK`。这是开发联调，不冒充公开依赖已经升级。RPC仓库独立的 `policy_e2e` 门禁验证三个实际进程、旧核心拒绝及经受限MITM的网关链路；既有 `workspace_e2e` 门禁继续验证正式依赖与默认MITM链路。

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

公开依赖环境运行普通go test/vet/build验证默认兼容与旧核心拒绝。显式开发工作区下的RPC `go test -count=3 -tags policy_e2e -timeout 180s ./integration/policy` 验证三入口拒绝、正向H1/H2、凭据不回显、配置快照及单并发名额复用；该门禁要求绝对GOWORK，缺少新核心时直接失败，不静默跳过。RPC新增只读手动Action记录精确源提交，并在三平台/双Go版本中运行此门禁。提交推送前不能视为远端已验收。

本批不新增正式版本。MITM独立地址策略及该模式的请求缓存限额已在后续P03-B3开发源码实现；Fetch/RPC可配置请求大小、连接/隧道配额、完整审计与FFI完整Spec输入仍未实现。目标服务自身的转发、路由/NAT及业务权限需要应用和网络层另行约束。
