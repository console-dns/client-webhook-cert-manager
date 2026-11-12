# 部署与运维

安装入口见 [README](../README.md)，完整 Helm 参数见 [values.yaml](../deploy/client-webhook-cert-manager/values.yaml)。

## 部署要求

集群需要预先安装 cert-manager，并启用 Kubernetes API aggregation。安装 chart 需要创建 APIService、ClusterRole/Binding，以及指定命名空间 Role/Binding 的权限。

chart 使用独立的 SelfSigned/CA Issuer 生成 webhook HTTPS 服务证书，由 cert-manager cainjector 填充 APIService 的 CA。该过程不依赖 Console DNS solver 完成证书签发。Service 默认使用 443 端口，容器监听 8443。

每个 API group 安装一个 webhook 实例。安装多个实例时应分别设置 `groupName`，并在对应 Issuer 中使用匹配的值。

## 凭据与证书轮换

token 从 cert-manager 提供的 `ResourceNamespace` 读取，不接受 solver 配置中指定任意 Secret namespace。每次操作重新读取 Secret，支持 token 轮换。

自定义 CA Secret 必须位于 webhook 部署命名空间。CA 在进程启动时加载，更新 Secret 后需重启 Deployment：

```bash
kubectl -n cert-manager rollout restart deployment \
  -l app.kubernetes.io/instance=console-dns-webhook
```

默认验证 DNS API TLS 证书。`dnsAPI.insecureSkipTLSVerify` 和自定义 CA 配置互斥；TLS 设置对该实例处理的所有 Issuer 生效。

## 环境变量

直接运行容器时，可使用以下环境变量；Helm 安装会根据 values 自动配置。

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `GROUP_NAME` | `acme.console-dns.github.com` | 聚合 API group，必须与 APIService 和 Issuer 一致 |
| `DNS_API_TIMEOUT` | `10s` | 单个请求的总超时，必须大于零且不超过 `1m` |
| `DNS_API_INSECURE_SKIP_TLS_VERIFY` | `false` | 跳过 DNS API TLS 证书链和主机名校验 |
| `DNS_API_CA_FILE` | 空 | PEM CA 文件路径；为空时使用系统 CA |

## 故障排查

### APIService 未就绪

```bash
kubectl describe apiservice v1alpha1.acme.console-dns.github.com
kubectl -n cert-manager get pods,svc,certificate,issuer \
  -l app.kubernetes.io/instance=console-dns-webhook
kubectl -n cert-manager logs \
  -l app.kubernetes.io/instance=console-dns-webhook --tail=100
```

检查 cert-manager/cainjector 是否正常、webhook 服务证书是否签发，以及 Kubernetes API server 是否能够访问 webhook Service。

### Challenge 持续等待或失败

```bash
kubectl -n default get certificate,order,challenge
kubectl -n default describe challenge <challenge-name>
```

确认 `groupName` 和 `solverName` 与安装配置一致，token Secret 位于正确命名空间，且 chart 的 `secretAccess` 授权了该 Secret。token 需要对验证目标 TXT 拥有 `rw` 权限。

如果 TXT 已写入但传播检查失败，检查权威 DNS 查询结果和 Console DNS 的记录发布链路。API 写入成功不代表 TXT 已经可以从公网查询。

### TLS 错误

确认 DNS API 的主机名、证书链和有效期。自签证书可以通过自定义 CA Secret 建立信任，或显式启用 `dnsAPI.insecureSkipTLSVerify=true`。两项不能同时设置。

### GHCR 返回未授权

GHCR 包的可见性独立于源码仓库。访问受限包时，使用具有 `read:packages` 权限的凭据执行 `helm registry login ghcr.io`；镜像拉取还需要配置相应的 Kubernetes 镜像拉取凭据。维护者应检查镜像和 chart 两个包的可见性，公开安装需要两者均为 public。参见 [GitHub 包权限文档](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility)。

## 已知限制

### DNS 名称与 CNAME 委派

通过 Issuer 的 `dns01.cnameStrategy: Follow` 启用 CNAME 委派。webhook 使用 cert-manager 的 `ResolvedFQDN` 和 `ResolvedZone`，token 权限需要覆盖委派后的目标 zone。

当前 Console DNS API 不支持 `@` 或空名称的区域根记录。如果委派后的验证目标等于 zone 本身，solver 返回明确错误。常规 `_acme-challenge` 和多标签相对名称受支持，zone 应统一使用小写。

### 重试与清理

Present 读取现有 TXT，相同验证值已存在时直接成功，否则追加并核对结果。CleanUp 只删除当前 challenge key，使用服务端真实 TTL，保留同名其他值，并处理重复副本。

重复清理使用已知的缺失响应和权限检查区分“已不存在”与“无权删除”。未知错误交由 cert-manager 重试；单次清理请求次数有上限。

### 客户端与后端约束

固定 SDK 无法注入 HTTP client/context。程序在启动时安装一次默认 HTTP client，只对 SDK 的 User-Agent 使用专用 transport。单个请求具有总超时，响应大小受限，DNS API 重定向被拒绝；不能完整透传每次 webhook 调用的取消信号。

已审阅的公开服务端提交 `93861e1` 会追加重复 TXT、按内容删除第一条，并主要在退出时持久化。GET 会按权限过滤记录，空结果可能是 `200 null`。这些行为已纳入契约测试，完整分析见[评估记录](assessment.md)。

provider 在本进程内串行处理 DNS 操作，chart 默认单副本和 `Recreate` 更新。该措施不能协调其他客户端或多个 webhook 部署；并发安全和持久化仍需由后端保证。

### 验证范围

自动化验证覆盖本地 DNS API 契约、TLS 行为、独立 envtest 控制平面，以及 cert-manager 官方基础和严格扩展测试。真实部署版本、DNS 发布链路和 ACME staging 签发需要在专用测试区域验证。构建所用 cert-manager/Kubernetes Go 模块版本不等同于经过验证的集群兼容版本范围。
