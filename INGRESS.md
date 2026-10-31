# RPC 接入资源边界（v0.1.2）

本页参数已包含在正式 v0.1.2；历史 v0.1.0 不包含这些参数。配置只来自可信启动参数，不接受 JSON、Protobuf 或请求头覆写；独立发布依赖构建也可使用，不依赖新核心的地址策略能力。非法值在监听前拒绝，新增名额默认均为 0，保持原有不限制行为。

## 两个入口的共同名额

- `-max-connections N`：0..65536。每个服务进程已经接受并登记的 TCP 连接总数，包含未完成 HTTP/HTTP2 握手、头部、慢正文、keepalive、活动请求和健康检查。满额在 Accept 后立即关闭新连接，不排队，不伪称已返回 HTTP 503 或 gRPC 状态。内核 backlog 和拒绝过程中的临时套接字不属于该计数。
- `-max-admitted-requests N`：0..65536。与已有 fetch 执行名额独立；两个 CLI 是独立进程，不共享一个全局池。
- 已有 `-max-inflight` 与完整 `-max-request-bytes` 预算继续生效，后者默认 4 MiB、最大 64 MiB，不能被单次请求参数解除。

TCP 名额在底层连接关闭时幂等归还。健康检查也消耗名额，满额的健康请求失败不自动表示服务进程故障。这些边界不等于总 RSS、内核连接数或每秒请求数限制。

## JSON-RPC：读取正文之前申请

新请求名额在 HTTP 头解析完成后、JSON-RPC 整体正文缓存及反序列化之前申请，覆盖 handler 的完整处理过程，所有路径与 OPTIONS/health 都计入。满额返回 HTTP 503，JSON-RPC 2.0 错误 `-32000`、Data 为 `overloaded`、id 为 null；未读取正文所以不能回显调用 ID。关闭本次连接并停止未读正文的自动排空，拒绝不等待慢上传者。

- `-max-header-bytes N`：0..16 MiB。0 保留 net/http 默认，非零采用 `MaxHeaderBytes` 解析预算及标准库缓冲余量，不是原始线上字节的精确 N/N+1 截止。超限通常在业务之前返回 431。
- `-read-header-timeout D`：默认 10s；0 选择原有 10s；允许至 24h。
- `-read-timeout D`：默认 0 不增加正文读取时限；非零为每请求头部和正文读取的总截止时间，不改变出站 fetch timeout，也不是按每个数据包续期。
- `-idle-timeout D`：默认 0 采用 net/http 的回退规则，可能回退到 read-timeout；非零限制 keepalive 等待下一请求的时间。最大 24h。

读取超时可能先由协议层关闭连接，或由 JSON-RPC 返回解析错误；不把所有阶段统一成 408，更不把半条请求当成成功 fetch。新名额仍在 HTTP 头解析之后，所以解析前保护还需 TCP 名额、头部预算和读头时限。

## gRPC：消息解压与反序列化之前申请

全局请求名额通过固定 gRPC 版本的 `InTapHandle` 在新流头部通过后申请，先于消息前缀、解压和 Protobuf 反序列化；包含 Health、Fetch 和尚未发送正文的调用。满额返回 gRPC `ResourceExhausted`，不等待完整消息。普通 unary handler 结束时归还；读/解码失败或取消时，已识别方法必须等处理清理结束后归还，不能仅因收到了 RST 就提前释放仍在运行的解码名额。未知方法由流关闭清理；传输的后续退出可异步完成。

头部解析已经先于此名额发生，不能把它声称为 HPACK 解码前边界。并发头部和待关闭响应流仍需结合连接数、每连接流数和头部预算控制。handler 后的响应序列化不占此请求名额；HTTP/2 流限制继续覆盖尚未关闭的传输流。

- `-max-streams-per-connection N`：0..65536。0 保留 gRPC 默认，非零设置并通告 HTTP/2 `MAX_CONCURRENT_STREAMS`。合规客户端可能在客户端排队；忽略通告的 N+1 流会被 `REFUSED_STREAM` 拒绝，不混称为全局名额的 `ResourceExhausted`。
- `-max-header-bytes N`：0..16 MiB。0 保留 gRPC 默认；非零采用 HTTP/2 解压后 header-list 大小，包含伪头、名字/值及每条 32 字节开销，与 HTTP/1 预算口径不同。超限由传输层拒绝，可能表现为流重置，客户端也可能根据通告在发送前报错。
- `-connection-timeout D`：0 保留 gRPC 默认 120s；非零限制连接建立及 HTTP/2 握手，最大 24h。
- `-rpc-timeout D`：默认 0 不额外增加服务端截止时间；非零从通过接入钩子开始，覆盖等待消息、解压/处理及其上下文，最大 24h，客户端更早的 deadline 仍优先。半条消息也会超时；到期取消出站请求并返回 `DeadlineExceeded`，不把执行器封装的失败当成正常 RPC 结果。取消发出与清理完成不是同一时刻，也不承诺强行中断任意 CPU 运算。

正式 CLI 显式注册 gzip 消息解码；`MaxRecvMsgSize` 同时沿用已有压缩消息长度和解压后消息检查。此前仅测试文件注册 gzip，不足以证明 CLI 支持，本批已用真实 CLI 验证精确大小、N+1、压缩消息和失败后的名额归还。

## 停机

保持原有 5 秒宽限期。到期后从独立接入连接表关闭全部套接字，再强制停止服务，覆盖尚未完成 HTTP/2 握手、还未进入 gRPC 传输表的连接。强制清理保留错误和非零退出，不表述为优雅完成。

## 示例与验收

```sh
fastls-jsonrpc -addr 127.0.0.1:8801 -max-connections 128 -max-admitted-requests 16 -max-inflight 8 -max-header-bytes 65536 -read-header-timeout 5s -read-timeout 30s -idle-timeout 30s
fastls-grpc -addr 127.0.0.1:8802 -max-connections 64 -max-admitted-requests 16 -max-inflight 8 -max-streams-per-connection 8 -max-header-bytes 65536 -connection-timeout 5s -rpc-timeout 30s
```

普通全包测试包含独立原始 HTTP/2 帧客户端：未发送正文时的超额拒绝、跨连接共享名额、N/N+1 流、精确 header-list 字节、RST、坏压缩消息、deadline 和健康恢复。`workspace_e2e` 的 `TestWorkspaceIngressBinaries` 再验证真实三入口 CLI；POSIX 用例验证有未读正文/未完成握手时的真实信号退出，Windows 不把进程强制终止算作等价证据。

协调 `Workspace integration` 的 `development_policy` 模式以完整 SHA 执行三平台、双 Go、三轮 CLI 检查，独立保留公开依赖与新核心地址策略门禁。S2 默认 MITM 契约、统一审计、速率和全实例累计流量预算仍属后续工作。
