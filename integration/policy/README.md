# 显式新核心策略联调

本组通过真实 Fetch、JSON-RPC、gRPC 与 MITM 进程验证允许/拒绝、跳转重检、代理拒绝、配额归还和受限 MITM 网关。它还单独以 `GOWORK=off` 构建旧核心服务，验证显式启用策略时在监听前失败。公开依赖下通过不能代替策略已生效的验证。

本机已验证的新核心输入为 Fastls `cb551d82fe4a3bc51e848b36d22180fb9b0aa798`、ftransport `d5afcd1a2db48a40850363b76e8a19eb339c757c` 与 imitate `fd10ea4393408e904ae2866ecbfe3efaaaa6ad8e`。服务输入使用本次整合分支，并记录实际完整 SHA，不使用历史开发报告替代。

将 `fastls`、`fastls/profile`、`ftransport`、`imitate` 与 `../workspace/README.md` 所列三个服务目录加入独立 `go.work`，然后在 RPC 仓库运行：

```sh
GOWORK=/absolute/path/to/go.work go test -count=3 -tags policy_e2e -timeout 300s ./integration/policy
```

MITM 仓库另外运行 `-tags mitm_policy_e2e ./...` 和对应竞态检查。公开模块清单不加入 `replace`，不升级依赖版本来隐藏两种构建的区别。

手动工作流 `address-policy-integration.yml` 要求五个外部仓库输入均为完整 40 位提交 SHA；Fetch/MITM 不提供可能缺少能力的 main 默认值。RPC 自身使用启动工作流选定的 ref，并将实际 SHA 与全部测试日志一并保存。工作流只运行测试，不创建 Release。远端三平台、双 Go 矩阵须单独通过，Windows 本机记录不构成该矩阵认证。
