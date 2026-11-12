# Console DNS Webhook for cert-manager

[![CI](https://github.com/console-dns/client-webhook-cert-manager/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/console-dns/client-webhook-cert-manager/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/console-dns/client-webhook-cert-manager)](https://github.com/console-dns/client-webhook-cert-manager/releases)
[![Go](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go)](go.mod)
[![License](https://img.shields.io/github/license/console-dns/client-webhook-cert-manager)](LICENSE)

使用 [Console DNS](https://github.com/console-dns) 完成 [cert-manager](https://cert-manager.io/) 的 ACME DNS01 验证，自动申请和续期普通域名及通配符证书。

[快速开始](#快速开始) · [配置](#配置) · [文档](#文档) · [更新记录](CHANGELOG.md)

## 功能

- 通过标准 cert-manager DNS01 webhook 接口创建、查询和清理验证 TXT。
- 支持重复调用、同名多个验证值共存，以及按 challenge 值精确清理。
- 支持 Issuer、ClusterIssuer 和 CNAME 委派。
- 使用 Kubernetes Secret 保存凭据，支持 token 轮换和按名称授权读取。
- 支持自定义 CA、跳过 DNS API TLS 证书校验及请求超时配置。
- 提供 OCI Helm chart 和 Linux amd64/arm64 容器镜像。

## 快速开始

### 前置条件

- 已安装 cert-manager，且 controller 和 cainjector 正常运行。
- Kubernetes 集群已启用 API aggregation。
- Helm 3.8 或更新版本，以及能够访问集群的 `kubectl`。
- 可访问的 Console DNS API，以及对验证名称 TXT 记录拥有 `rw` 权限的 token。

以下步骤使用默认的 `cert-manager` 命名空间和 ServiceAccount。自定义安装位置请参阅[配置](#配置)。

### 1. 安装 webhook

```bash
helm upgrade --install console-dns-webhook \
  oci://ghcr.io/console-dns/charts/client-webhook-cert-manager \
  --version 0.1.0 \
  --namespace cert-manager --create-namespace
```

等待 webhook 的聚合 API 就绪：

```bash
kubectl wait --for=condition=Available \
  apiservice/v1alpha1.acme.console-dns.github.com --timeout=120s
```

### 2. 创建凭据 Secret

将 token 保存到本地文件，并创建 Secret：

```bash
kubectl -n cert-manager create secret generic console-dns-api-token \
  --from-file=token=/path/to/token-file
```

token 必须具有目标 TXT 的 `rw` 权限；`ru` 可以创建记录，但不能完成清理。不要将实际 token 提交到 Git。

### 3. 配置 Issuer 并申请证书

下载 [ClusterIssuer 示例](examples/clusterissuer.yaml) 和 [Certificate 示例](examples/certificate.yaml)，分别保存为 `clusterissuer.yaml` 和 `certificate.yaml`，然后修改邮箱、DNS API 地址和域名。

```bash
kubectl apply -f clusterissuer.yaml
kubectl apply -f certificate.yaml
kubectl -n default get certificate,order,challenge
```

示例使用 Let's Encrypt staging。验证流程通过后，切换到正式 ACME 服务地址并使用独立的 ACME account Secret。需要命名空间级配置时，使用 [Issuer 示例](examples/issuer.yaml)。

## 配置

### DNS01 solver

在 Issuer 或 ClusterIssuer 的 `spec.acme.solvers` 中配置 webhook：

```yaml
dns01:
  webhook:
    groupName: acme.console-dns.github.com
    solverName: console-dns
    config:
      server: https://dns-api.example.com
      tokenSecretRef:
        name: console-dns-api-token
        key: token
      ttl: 60
```

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `groupName` | `acme.console-dns.github.com` | 必须与 Helm chart 的 `groupName` 一致 |
| `solverName` | `console-dns` | 固定的 solver 名称 |
| `config.server` | 必填 | HTTP/HTTPS API 基地址，可含路径前缀，不包含 `/api/v1/zones`、凭据或查询参数 |
| `config.tokenSecretRef.name` | 必填 | 保存 API token 的 Secret 名称 |
| `config.tokenSecretRef.key` | 必填 | Secret 中的 token 字段 |
| `config.ttl` | `60` | TXT TTL，单位为秒，范围为 1–86400 |

Issuer 的凭据 Secret 位于 Issuer 所在命名空间；ClusterIssuer 的凭据位于 cert-manager 的 cluster resource namespace，默认是 `cert-manager`。

### Helm values

完整参数见 [values.yaml](deploy/client-webhook-cert-manager/values.yaml)。常用配置如下：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `groupName` | `acme.console-dns.github.com` | webhook API group |
| `dnsAPI.timeout` | `10s` | 单个 DNS API 请求超时，必须大于零且不超过 `1m` |
| `dnsAPI.insecureSkipTLSVerify` | `false` | 跳过 DNS API 证书链和主机名校验 |
| `dnsAPI.caBundleSecretRef.name` | 空 | 自定义 CA Secret 名称 |
| `dnsAPI.caBundleSecretRef.key` | `ca.crt` | CA Secret 中的 PEM 字段 |
| `certManager.namespace` | `cert-manager` | cert-manager controller 所在命名空间 |
| `certManager.serviceAccountName` | `cert-manager` | controller 的 ServiceAccount |
| `secretAccess` | `cert-manager/console-dns-api-token` | 允许读取的凭据 Secret 列表 |
| `replicaCount` | `1` | webhook 副本数 |
| `updateStrategy` | `Recreate` | Deployment 更新策略 |
| `image.tag` | chart 的 `appVersion` | 镜像版本，可用 `image.digest` 固定摘要 |

将自定义配置保存为 `values.yaml`，安装或升级时使用 `-f values.yaml`。

### 跳过 TLS 证书校验

连接使用自签证书的 DNS API 时，可以显式跳过证书校验：

```bash
helm upgrade --install console-dns-webhook \
  oci://ghcr.io/console-dns/charts/client-webhook-cert-manager \
  --version 0.1.0 --namespace cert-manager \
  --set dnsAPI.insecureSkipTLSVerify=true
```

该设置仅作用于本实例处理的 Console DNS API 请求。Kubernetes API 客户端和 webhook APIService 的证书校验保持开启。

### 使用自定义 CA

在 webhook 所在命名空间创建 CA Secret：

```bash
kubectl -n cert-manager create secret generic console-dns-api-ca \
  --from-file=ca.crt=/path/to/ca.crt
```

在 Helm values 中配置：

```yaml
dnsAPI:
  insecureSkipTLSVerify: false
  caBundleSecretRef:
    name: console-dns-api-ca
    key: ca.crt
```

自定义 CA 与跳过证书校验不能同时启用。更新 CA Secret 后需要重启 webhook Deployment；更新 API token Secret 无需重启。

### 授权多个命名空间

chart 只授予指定 Secret 的 `get` 权限。以下配置同时支持默认 ClusterIssuer 和 `applications` 命名空间的 Issuer：

```yaml
secretAccess:
  - namespace: cert-manager
    secretNames:
      - console-dns-api-token
  - namespace: applications
    secretNames:
      - tenant-dns-token
```

这些命名空间需要预先存在。自定义 cert-manager 安装位置时，同时调整 `certManager.namespace`、`certManager.serviceAccountName` 和 `secretAccess`。

## 发布产物

| 产物 | 地址 |
| --- | --- |
| 容器镜像 | `ghcr.io/console-dns/client-webhook-cert-manager:0.1.0` |
| OCI Helm chart | `oci://ghcr.io/console-dns/charts/client-webhook-cert-manager`，版本 `0.1.0` |
| Release 与 chart 压缩包 | [GitHub Releases](https://github.com/console-dns/client-webhook-cert-manager/releases) |

镜像提供 `linux/amd64` 和 `linux/arm64` 架构，以非 root 身份运行。

## 文档

- [部署、故障排查与已知限制](docs/operations.md)
- [开发、测试与发布流程](docs/development.md)
- [配置示例](examples/)
- [更新记录](CHANGELOG.md)
- [实现前评估](docs/assessment.md)

webhook 支持通过 `dns01.cnameStrategy: Follow` 使用 CNAME 委派。当前后端不支持委派到 zone 根记录；chart 默认单副本，以配合已审阅后端的并发约束。详情见[已知限制](docs/operations.md#已知限制)。

## 开发与贡献

开发需要 Go 1.26+ 和 Helm 3.10+；构建容器镜像还需要 Docker。

```bash
git clone https://github.com/console-dns/client-webhook-cert-manager.git
cd client-webhook-cert-manager
make test
make vet
make build
make helm-test
```

Go module 为 `github.com/console-dns/client-webhook-cert-manager`，固定依赖 `github.com/console-dns/client v0.0.0-20250418013039-135fd950cbe2`。集成测试和本地打包方法见[开发文档](docs/development.md)。

欢迎提交 [Issue](https://github.com/console-dns/client-webhook-cert-manager/issues) 或 Pull Request。报告问题时请附上版本、已脱敏的配置、复现步骤和相关日志；提交代码前请运行上述检查。

## 许可证

本项目使用 [Apache License 2.0](LICENSE)。
