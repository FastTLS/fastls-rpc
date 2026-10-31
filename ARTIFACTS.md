# 发布产物与来源证明

## 范围与内容

`Development artifacts` 工作流在手动触发时提供保留 14 天的开发构建；推送 `v*` 标签时，它会在验证成功后创建正式 GitHub Release。不会发布到 GitHub 之外的渠道。

每个 Linux、Windows、macOS 的 amd64/arm64 归档都包含：

- `fastls-jsonrpc`（Windows 为 `.exe`）
- `fastls-grpc`（Windows 为 `.exe`）
- `BUILD_INFO.txt`

开发归档名包含 `dev-<commit前12位>`，正式归档名包含版本标签。

## 下载

正式版本从 GitHub Releases 页面下载。开发版本从 Actions 页面下载 `fastls-rpc-development-<完整提交SHA>`，或使用：

```bash
gh run list --repo FastTLS/fastls-rpc --workflow development-artifacts.yml
gh run download RUN_ID \
  --repo FastTLS/fastls-rpc \
  --name fastls-rpc-development-COMMIT_SHA \
  --dir fastls-rpc-artifacts
```

下载目录包含六个平台归档、`SHA256SUMS` 和 `provenance.sigstore.json`。

## 校验摘要

```bash
cd fastls-rpc-artifacts
sha256sum --check SHA256SUMS
```

macOS 可用 `shasum -a 256`，PowerShell 可用 `Get-FileHash -Algorithm SHA256` 逐项对照。

## 验证签发来源

在线验证：

```bash
gh attestation verify fastls-rpc-dev-COMMIT-linux-amd64.tar.gz \
  --repo FastTLS/fastls-rpc \
  --signer-workflow FastTLS/fastls-rpc/.github/workflows/development-artifacts.yml
```

使用随包的证明副本：

```bash
gh attestation verify fastls-rpc-dev-COMMIT-linux-amd64.tar.gz \
  --repo FastTLS/fastls-rpc \
  --bundle provenance.sigstore.json \
  --signer-workflow FastTLS/fastls-rpc/.github/workflows/development-artifacts.yml
```

完全离线验证还需在联网环境准备 `gh attestation trusted-root > trusted_root.jsonl`，离线命令额外传入 `--custom-trusted-root trusted_root.jsonl`。

验证成功表示归档摘要和签发工作流身份匹配；只有位于正式 GitHub Release 且标签匹配的产物才是正式版本。

## 签发链路

工作流先校验 module、测试和静态检查，再用 `CGO_ENABLED=0`、`-trimpath` 和嵌入的提交信息构建两个服务。随后计算归档 SHA-256，使用 GitHub OIDC/Sigstore 生成 SLSA provenance，并用限定仓库及签发工作流的策略自验证后上传。

发行门禁接受同一完整提交的成功候选，或独立重新核验过的可信 main 祖先证明。复用要求源码、模块、生成输入、原生命令、工具链及平台配置一致，全部必需任务成功；未知差异、缺失、失败、取消和任意跳过均阻止签发。RPC 的跨仓契约单独检查固定引用、协议测试与执行配置，不随普通 Go 证明自动豁免。归档继续按实际生产提交构建、记录 vcs.revision 并签名，不将旧字节改标为新提交。main 不自动重复生成开发归档，正式标签每个平台只构建一次。
