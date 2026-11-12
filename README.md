# Console DNS cert-manager webhook

为 cert-manager 提供 Console DNS 的 DNS01 challenge solver，支持 TXT 幂等创建、按 challenge 值清理、Secret 凭据轮换，以及可配置的 DNS API TLS 校验。Go module 为 `github.com/console-dns/client-webhook-cert-manager`。

固定使用以下客户端版本，所有 DNS API 请求均由这个依赖发出：

```text
github.com/console-dns/client v0.0.0-20250418013039-135fd950cbe2
```

构建基线为 Go 1.26、cert-manager Go 模块 v1.21.3 与 Kubernetes Go 模块 v0.36.2。集群需预先安装 cert-manager，并启用 Kubernetes API aggregation。构建依赖版本不代表已测试所有 Kubernetes/cert-manager 运行版本。

## 安装

镜像和 chart 通过版本标签发布到 GHCR：

| 产物 | 地址 |
| --- | --- |
| 容器镜像 | `ghcr.io/console-dns/client-webhook-cert-manager:<version>` |
| OCI Helm chart | `oci://ghcr.io/console-dns/charts/client-webhook-cert-manager` |

下面命令适用于 `v0.1.0` 发布流水线成功后。首次发布前可以使用仓库内的本地 chart 配合自行构建的镜像。

```bash
helm upgrade --install console-dns-webhook \
  oci://ghcr.io/console-dns/charts/client-webhook-cert-manager \
  --version 0.1.0 \
  --namespace cert-manager --create-namespace
```

chart 默认允许 webhook 读取 `cert-manager/console-dns-api-token`，并为 cert-manager 的 `cert-manager` ServiceAccount 授予调用 solver 的权限。安装 chart 需要创建 APIService、ClusterRole/Binding 和指定命名空间 Role/Binding 的权限。

创建 API token Secret。token 需要对验证名称的 TXT 拥有 Console DNS `rw` 权限；`ru` 能创建，但不能删除。

```bash
kubectl -n cert-manager create secret generic console-dns-api-token \
  --from-file=token=/path/to/token-file
```

将 [ClusterIssuer 示例](examples/clusterissuer.yaml) 中的邮箱、DNS API 地址和域名替换为实际值，再应用配置；示例使用 ACME staging。随后修改并应用 [Certificate 示例](examples/certificate.yaml)，申请普通域名和通配符证书。正式签发时切换 ACME 服务地址并使用独立的 ACME account Secret。

```bash
kubectl apply -f examples/clusterissuer.yaml
kubectl apply -f examples/certificate.yaml
kubectl get apiservice v1alpha1.acme.console-dns.github.com
kubectl -n default get certificate,order,challenge
```

## DNS API TLS 配置

默认验证 DNS API 的 TLS 证书。连接使用自签证书的 DNS API 时，可以显式跳过证书链和主机名校验：

```bash
helm upgrade --install console-dns-webhook \
  oci://ghcr.io/console-dns/charts/client-webhook-cert-manager \
  --version 0.1.0 --namespace cert-manager \
  --set dnsAPI.insecureSkipTLSVerify=true
```

该选项只作用于本进程发往 Console DNS API 的客户端请求；不会关闭 Kubernetes API 客户端校验，也不会改变 APIService 的 CA 校验。它是部署级设置，作用于该 webhook 实例处理的所有 Issuer。DNS API 重定向会被拒绝。

也可以提供自定义 CA 并保持证书校验开启：

```bash
kubectl -n cert-manager create secret generic console-dns-api-ca \
  --from-file=ca.crt=/path/to/ca.crt
```

```yaml
dnsAPI:
  timeout: 10s
  insecureSkipTLSVerify: false
  caBundleSecretRef:
    name: console-dns-api-ca
    key: ca.crt
```

CA Secret 应位于 webhook 部署命名空间。修改 CA 内容后重启 Deployment，使进程重新加载信任库。API token 则在每次操作时读取，不需要重启。

## Issuer 配置

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

| 字段 | 行为 |
| --- | --- |
| `groupName` | 必须与 chart 的 `groupName` 一致 |
| `solverName` | 固定为 `console-dns` |
| `config.server` | 必填，HTTP/HTTPS API 基地址，可含路径前缀；不要包含 `/api/v1/zones`、凭据、查询参数或 fragment |
| `config.tokenSecretRef` | 必填，只接受 Secret name/key |
| `config.ttl` | 默认为 60 秒，允许 1–86400 |

对于 Issuer，token Secret 位于 Issuer 命名空间；对于 ClusterIssuer，位于 cert-manager 的 cluster resource namespace（默认 `cert-manager`）。使用 cert-manager 提供的 `ResourceNamespace`，不接受配置中任意指定 Secret namespace。示例见 [Issuer](examples/issuer.yaml)。

通过 chart 配置逐个授权 Secret；每个条目的命名空间必须已存在。以下配置支持默认 ClusterIssuer 和 `applications` 命名空间的 Issuer：

```yaml
secretAccess:
  - namespace: cert-manager
    secretNames:
      - console-dns-api-token
  - namespace: applications
    secretNames:
      - tenant-dns-token
```

读取权限仅包含指定名称的 Secret `get`，不授予全局 Secret 列表权限。更改 cert-manager 安装位置时，相应设置 `certManager.namespace`、`certManager.serviceAccountName` 和 `secretAccess`。

CNAME 委派由 cert-manager 的 `dns01.cnameStrategy: Follow` 启用。webhook 使用 `ResolvedFQDN`/`ResolvedZone`，因此 token 权限应覆盖委派后的目标 zone。当前 Console DNS API 不支持 `@`/空名称的区域根记录；若解析后的验证目标等于 zone 本身，solver 返回明确错误。常规 `_acme-challenge` 和多标签相对名称均受支持，zone 建议使用小写。

## 重试、并发与后端约束

Present 读取现有 TXT，相同验证值已存在时成功返回，否则追加并核对结果。CleanUp 只删除当前 challenge key，使用服务端真实 TTL，保留同名其他值，并处理重复副本。重复清理通过已知的删除缺失响应和权限检查区分“已不存在”与“无权删除”。未知错误会保留为失败交给 cert-manager 重试。

指定 SDK 无法注入 HTTP client/context，因此程序在启动时安装一次默认 HTTP client，并只对 SDK 的 User-Agent 路由专用 TLS transport。单个请求具有总超时，响应大小受限；无法完整透传每个 webhook 请求的取消信号。环境变量如下：

| 环境变量 | 默认值 |
| --- | --- |
| `GROUP_NAME` | `acme.console-dns.github.com` |
| `DNS_API_TIMEOUT` | `10s`，必须大于零且不超过 `1m` |
| `DNS_API_INSECURE_SKIP_TLS_VERIFY` | `false` |
| `DNS_API_CA_FILE` | 空，使用系统 CA |

为降低当前公开服务端共享 map/slice 无锁的风险，provider 在本进程内串行处理 DNS 操作；chart 默认单副本、Recreate 更新。该措施不能协调其他客户端或多个 webhook 部署。服务端并发安全和持久化仍需由后端保证。

审阅的公开服务端 `93861e1` 会追加重复 TXT、按内容删除第一条，并主要在退出时持久化。它的 GET 还会按权限过滤记录，空结果可能是 `200 null`。这些行为已纳入本地契约测试；真实部署版本和 DNS 发布链路仍应使用专用测试区域验证。完整背景见 [评估记录](docs/assessment.md)。

## 开发与验证

```bash
make test
make vet
make build
make helm-lint
make helm-test
make chart-package
```

单元及 HTTP 契约测试使用本地模拟服务，覆盖 TLS 校验/跳过、自定义 CA、超时、重定向、权限过滤、重复记录及精确清理。integration 测试使用 cert-manager 官方基础/严格扩展测试，启动独立 envtest API server 和本地 DNS/API 服务，不使用当前 kubeconfig，也不访问真实 DNS 区域。首次运行集成测试需下载工具及测试用控制平面：

```bash
go install sigs.k8s.io/controller-runtime/tools/setup-envtest@v0.0.0-20260511180552-3be3f1bf2b2f
asset_dir="$(setup-envtest use -p path 1.36.2)"
export TEST_ASSET_ETCD="${asset_dir}/etcd"
export TEST_ASSET_KUBE_APISERVER="${asset_dir}/kube-apiserver"
export TEST_ASSET_KUBECTL="${asset_dir}/kubectl"
make integration-test
```

确保 Go 的可执行文件安装目录位于 PATH 中。CI 自动准备这些依赖。

镜像入口为 `./cmd/webhook`，以非 root 身份运行，服务端口 8443。chart 使用独立 SelfSigned/CA Issuer 为 webhook 服务证书签名，再由 cert-manager cainjector 填充 APIService CA。

## CI 与 OCI 发布

GitHub Actions 对 main 推送和 pull request 运行格式、固定依赖、Go race 测试、vet、构建、Helm 校验及容器构建，并运行独立的 integration 测试。发布工作流由 `v<semver>` 标签触发，验证通过后发布 amd64/arm64 镜像和 OCI chart。

例如发布版本 `v0.1.0` 时，镜像标签为 `0.1.0`，chart 版本与 appVersion 也为 `0.1.0`。发布使用仓库的 `GITHUB_TOKEN`，需要组织允许该仓库创建 GHCR packages。GHCR 包若未继承公开仓库权限，首次发布后需在包设置中配置可见性；私有包安装需要先执行 `helm registry login ghcr.io`。

```bash
git tag v0.1.0
git push origin v0.1.0
```

本地也可以打包和上传 chart，OCI push 地址不包含 chart 名或版本：

```bash
helm package deploy/client-webhook-cert-manager --destination dist
helm push dist/client-webhook-cert-manager-0.1.0.tgz oci://ghcr.io/console-dns/charts
```
