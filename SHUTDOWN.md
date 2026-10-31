# 请求服务停机契约

## Fetch / RPC 0.3.0：可选协作停机

Fetch、JSON-RPC、gRPC 新增可信启动参数 `-shutdown-cancel-after 1s`。默认 `0` 关闭，保留五秒正常排空、超时强制失败；启用值必须大于 0 且小于 5s，非法值在监听前拒绝。该配置不接受请求 JSON、Protobuf 或请求头覆盖。

收到停止事件后立即停止接入，已开始的上游请求先正常排空；到指定间隔主动取消剩余请求，并等待处理器/解码、正文、上传源及并发名额实际释放。排空、取消、回收与审计共用停止时刻起的五秒绝对期限；`-audit-drain-timeout` 可以进一步缩短审计等待，不能重置总预算。正常完成和协作取消且回收成功退出 0；不合作处理器、未完成的传输握手、审计失败或最终强制兜底仍非零退出。关闭模式保留原独立审计预算。

```sh
fastls-fetch -addr 127.0.0.1:8800 -shutdown-cancel-after 1s -audit-file /private/fetch.jsonl
fastls-jsonrpc -addr 127.0.0.1:8801 -shutdown-cancel-after 1s
fastls-grpc -addr 127.0.0.1:8802 -shutdown-cancel-after 1s
```

取消保留原请求期限与调用方取消语义，不取消其它实例；已入站但仍在解码的请求不能在停止后新发上游。HTTP 慢正文通过读期限解除阻塞，不能中断的解码仍保留名额直到实际结束。取消结果沿用原通道：Fetch `ok=false/error_code=cancelled`，JSON-RPC 返回相同结果字段；gRPC 活动调用可返回 `Canceled`。传输已关闭时调用方也可能收到连接错误，不能承诺一定收齐响应。审计保留取消分类，不记录正文或敏感请求字段。

Unix 支持 SIGINT/SIGTERM；Windows 控制台支持 Ctrl+C/Ctrl+Break，实际验收使用独立隐藏控制台的 Ctrl+Break。没有增加 Windows 服务管理器集成、远程停止 API 或强杀后的优雅保证。三入口、H1/H2/H3、正常/协作/强制路径分别验证；Linux 一次五分钟混合协议观察之后追加停机，分别记录时长。MITM v0.1.2、核心固定版本、Python 0.6.0 / Node 0.15.0 保持，未增加新协议或平台。

## Fetch / RPC 0.3.0: optional cooperative shutdown

Fetch, JSON-RPC and gRPC add operator-owned `-shutdown-cancel-after 1s`. The default `0` disables it and preserves the five-second drain followed by a failing forced fallback. An enabled interval must be greater than zero and less than 5s; invalid values fail before listening. Request JSON, Protobuf and headers cannot override this setting.

A stop event closes admission immediately. Existing upstream work drains first; at the configured interval remaining requests are canceled and the server waits for actual handler/decoder, body, upload and permit release. Drain, cancellation, cleanup and auditing share one absolute five-second deadline measured from stop. `-audit-drain-timeout` may shorten audit waiting but cannot restart the budget. Completed work or cooperative cancellation with successful cleanup exits 0. Uncooperative work, incomplete transport handshakes, audit failure and forced fallback remain nonzero. Disabled mode retains the original separate audit budget.

```sh
fastls-fetch -addr 127.0.0.1:8800 -shutdown-cancel-after 1s -audit-file /private/fetch.jsonl
fastls-jsonrpc -addr 127.0.0.1:8801 -shutdown-cancel-after 1s
fastls-grpc -addr 127.0.0.1:8802 -shutdown-cancel-after 1s
```

Original deadlines and caller cancellation remain; other instances are isolated. Requests still decoding cannot start new upstream work after stop. HTTP body reads are interrupted with a read deadline; non-interruptible decoders retain permits until actual completion. Existing error channels remain: Fetch and JSON-RPC use `ok=false/error_code=cancelled`; an active gRPC call may return `Canceled`. A closed transport may instead surface a connection error, so delivery of a complete response is not guaranteed. Auditing retains cancellation classification without request bodies or sensitive fields.

Unix supports SIGINT/SIGTERM. Windows consoles support Ctrl+C/Ctrl+Break; acceptance uses real Ctrl+Break in a private hidden console. This adds no Windows service-manager integration or remote stop API and gives no graceful guarantee after a process kill. Three entries, H1/H2/H3 and normal/cooperative/forced paths are checked separately. One five-minute mixed-protocol Linux resource observation is followed by shutdown; timings are recorded separately. MITM v0.1.2, the pinned core and Python 0.6.0 / Node 0.15.0 remain unchanged, with no new protocol or platform.
