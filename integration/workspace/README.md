# 跨仓四进程联调

这组测试首先确认三个服务使用完全一致的 `fastls`、`ftransport`、`imitate` 和 `profile` 版本，并拒绝 `replace` 指令；随后从当前源码构建并启动 `fastls-fetch`、`fastls-jsonrpc`、`fastls-grpc` 和 `fastls-mitm`，再让三个服务入口通过同一个 MITM 访问本地 HTTPS/HTTP2 目标。它验证的是可发布依赖、外部进程和公开协议契约，不使用进程内处理器替代 CLI。

## 本地运行

将三个仓库放在同一目录，并使用以下目录名：

```text
fastls-fetch-ftransport/
fastls-rpc-ftransport/
fastls-mitm-ftransport/
```

在 RPC 仓库中运行：

```bash
GOWORK=off go test -count=3 -tags workspace_e2e ./integration/workspace
```

测试只监听环回地址，目标、CA、二进制和进程日志都位于测试临时目录，不访问公网。

## GitHub Actions

`workspace-integration.yml` 在 RPC 拉取请求和主分支相关代码变化后运行；另外两个仓库默认使用 `main`。手动运行时，可通过 `fetch_ref` 和 `mitm_ref` 指定分支、标签或完整提交；RPC 版本由启动工作流时选择的 ref 决定。

每次运行都会保存三个仓库的精确提交号、模块依赖清单和测试日志。候选分支尚未合并时，应显式填写另外两个仓库的候选 ref，避免把候选 RPC 与不匹配的主分支组合误判为兼容。RPC 拉取请求的自动门禁始终对照另外两个仓库的 `main`，因此也会约束跨仓合并顺序。
