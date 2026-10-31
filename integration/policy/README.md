# 显式新核心策略联调

本组通过真实 Fetch、JSON-RPC、gRPC 与 MITM 进程验证允许/拒绝、跳转重检、代理拒绝、配额归还和受限 MITM 网关。正式 v0.1.2 图使用 core `v0.1.3-0.20261030152832-0dc4eea0f32e`、profile `v0.1.1-0.20261030130219-e9e40f15c9ac`、ftransport `v0.1.2-0.20261030131050-d9812320808e`、imitate `v0.1.2-0.20261030131552-ebd18547d28a`；显式选择后逐个验证服务实际选中的版本和没有替换。另以临时独立 modfile 固定旧 core v0.1.1、ftransport/imitate v0.1.0，验证不支持策略时仍在监听前失败，不更改生产 go.mod/go.sum。不能把新公开版本误判为旧的不支持基线。

验证正式图时，在 RPC 仓库执行：

```sh
GOWORK=off FASTLS_POLICY_GRAPH=formal go test -v -count=1 -tags policy_e2e -timeout 480s ./integration/policy
```

显式模式或选中的版本不符合要求即失败，不静默退回本地 workspace。`Workspace integration` 在正式图模式收集全部策略日志后再上传证据；开发模式继续使用下面的固定源码工作区。

## 历史开发工作区（不代表当前正式图）

原开发批次本机已验证的核心输入为 Fastls `cb551d82fe4a3bc51e848b36d22180fb9b0aa798`、ftransport `d5afcd1a2db48a40850363b76e8a19eb339c757c` 与 imitate `fd10ea4393408e904ae2866ecbfe3efaaaa6ad8e`。服务输入使用本次整合分支，并记录实际完整 SHA，不使用历史开发报告替代。

将 `fastls`、`fastls/profile`、`ftransport`、`imitate` 与 `../workspace/README.md` 所列三个服务目录加入独立 `go.work`，然后在 RPC 仓库运行：

```sh
GOWORK=/absolute/path/to/go.work go test -count=3 -tags policy_e2e -timeout 300s ./integration/policy
```

MITM 仓库另外运行 `-tags mitm_policy_e2e ./...` 和对应竞态检查。公开模块清单不加入 `replace`，不升级依赖版本来隐藏两种构建的区别。

手动工作流 `address-policy-integration.yml` 要求五个外部仓库输入均为完整 40 位提交 SHA；Fetch/MITM 不提供可能缺少能力的 main 默认值。RPC 自身使用启动工作流选定的 ref，并将实际 SHA 与全部测试日志一并保存。工作流只运行测试，不创建 Release。远端三平台、双 Go 矩阵须单独通过，Windows 本机记录不构成该矩阵认证。

以下为历史注册流程，当前工作流已在 main：新工作流尚未进入默认分支时，GitHub 不登记其手动入口。此时从已有 `Workspace integration` 工作流选择候选 RPC 分支，填写 Fetch/MITM 完整 SHA，并开启 `development_policy`；它将复用本候选中的策略工作流，以固定的新核心/传输/imitate 输入同时执行公开依赖联调与开发策略六组矩阵。默认关闭此选项，原有自动门禁保持公开依赖范围。
