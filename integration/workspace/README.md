# 跨仓四进程联调

这组测试首先确认三个服务使用完全一致的 `fastls`、`ftransport`、`imitate` 和 `profile` 版本，并拒绝 `replace` 指令；随后从当前源码构建并启动 `fastls-fetch`、`fastls-jsonrpc`、`fastls-grpc` 和 `fastls-mitm`，再让三个服务入口通过同一个 MITM 访问本地 HTTPS/HTTP2 目标。负向契约还会验证无效配置的协议级错误、三种入口的 4 MiB 请求边界、上级代理拒绝 CONNECT 时不会静默直连、服务超时和 gRPC 调用取消向目标传播，以及失败后三个服务仍然健康。Linux/macOS 还会向四个真实进程发送中断信号，确认它们在时限内退出并关闭监听端口。它验证的是可发布依赖、外部进程和公开协议契约，不使用进程内处理器替代 CLI。

## 本地运行

P03-B4新增真实CLI的max-request-bytes验收：三个入口对非法值在监听前拒绝；配置1024字节后验证完整JSON/Protobuf消息的N/N+1边界，JSON包含尾部空白，超限不访问目标且后续请求仍成功。默认4MiB边界继续保留；压缩消息和chunked专项由各入口的真实传输单元回归覆盖。

将三个仓库放在同一目录，并使用以下目录名：

```text
fastls-fetch-ftransport/
fastls-rpc-ftransport/
fastls-mitm-ftransport/
```

在 RPC 仓库中运行：

```bash
GOWORK=off go test -count=1 -tags workspace_e2e ./integration/workspace
```

测试只监听环回地址，目标、CA、二进制和进程日志都位于测试临时目录，不访问公网。

`TestWorkspaceIngressBinaries` 单独构建真实 Fetch、JSON-RPC 和 gRPC CLI，验证接入/接收名额、慢头部与慢正文、空闲连接、协议错误、读取时限、压缩消息尺寸和恢复。启用 `development_policy` 的协调工作流在三平台、双 Go 组合均执行三轮；普通 Ubuntu 四进程门禁还执行持有未读正文/未完成 HTTP2 握手时的 POSIX 信号强制清理，要求有界退出并保留非零失败状态。具体计数口径见 [INGRESS.md](../../INGRESS.md)。

gRPC 入站还使用独立原始帧客户端，把 8 个 HTTP/2 SETTINGS 连续写入同一连接，并要求逐帧返回 8 个 ACK。它检查真实 CLI，不应用基准靶场的 Go net/http overlay；Fetch、JSON-RPC 和 MITM 的入站 HTTP/1 路径不据此声称支持 HTTP/2。

Windows 本地运行会跳过 POSIX 中断信号用例；协调 Action 的 Ubuntu runner 会执行该用例。Windows 上的生命周期实现仍由各仓库的 context 取消和监听器关闭测试覆盖，不把不等价的强制终止冒充优雅退出。

## GitHub Actions

`workspace-integration.yml` 在 RPC 拉取请求和主分支相关代码变化后运行；另外两个仓库默认使用 `main`。手动运行时，可通过 `fetch_ref` 和 `mitm_ref` 指定分支、标签或完整提交；RPC 版本由启动工作流时选择的 ref 决定。

每次运行都会保存三个仓库的精确提交号、模块依赖清单和测试日志。候选分支尚未合并时，应显式填写另外两个仓库的候选 ref，避免把候选 RPC 与不匹配的主分支组合误判为兼容。RPC 拉取请求的自动门禁始终对照另外两个仓库的 `main`，因此也会约束跨仓合并顺序。
