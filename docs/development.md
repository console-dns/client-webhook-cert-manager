# 开发与发布

## 构建依赖

- Go 1.26 或更新版本。
- Helm 3.10 或更新版本（模板测试使用 `--set-json`）。
- Docker：仅构建容器时需要。

Go module 为 `github.com/console-dns/client-webhook-cert-manager`。DNS API 请求固定使用以下依赖：

```text
github.com/console-dns/client v0.0.0-20250418013039-135fd950cbe2
```

构建基线使用 cert-manager Go 模块 v1.21.3 和 Kubernetes Go 模块 v0.36.2，完整版本见 [go.mod](../go.mod)。

## 本地检查

```bash
make test
make vet
make build
make helm-test
make chart-package
```

`make test` 启用 Go race detector。单元及 HTTP 契约测试使用本地模拟服务，覆盖名称转换、Secret 权限、TLS 校验/跳过、自定义 CA、超时、重定向、权限过滤、重复记录和精确清理。

编译产物位于 `bin/`，chart 压缩包位于 `dist/`。构建镜像：

```bash
make image VERSION=0.1.0
```

镜像入口为 `./cmd/webhook`，通过 `main.version` 注入版本。发布镜像支持 Linux amd64/arm64，以非 root 身份运行。

## 集成测试

集成测试使用 cert-manager 官方基础及严格扩展测试，启动独立 envtest API server 和本地 DNS/API 服务，不使用当前 kubeconfig，也不访问真实 DNS 区域。

先安装测试工具并下载控制平面二进制：

```bash
go install sigs.k8s.io/controller-runtime/tools/setup-envtest@v0.0.0-20260511180552-3be3f1bf2b2f
asset_dir="$(setup-envtest use -p path 1.36.2)"
export TEST_ASSET_ETCD="${asset_dir}/etcd"
export TEST_ASSET_KUBE_APISERVER="${asset_dir}/kube-apiserver"
export TEST_ASSET_KUBECTL="${asset_dir}/kubectl"
make integration-test
```

确保 Go 的可执行文件安装目录位于 PATH 中。CI 会自动准备这些依赖。

## CI

[CI 工作流](../.github/workflows/ci.yml) 对 main 推送和 pull request 执行：

- 模块文件一致性、指定客户端版本和格式检查。
- Go race 测试、vet 和构建。
- Helm lint、模板检查和容器构建。
- 独立的 envtest 集成测试。

## 发布版本

[发布工作流](../.github/workflows/release.yml) 由 `v<semver>` 标签触发。所有校验通过后发布：

| 产物 | 地址 |
| --- | --- |
| 容器镜像 | `ghcr.io/console-dns/client-webhook-cert-manager:<version>` |
| OCI chart | `oci://ghcr.io/console-dns/charts/client-webhook-cert-manager` |
| Release | GitHub Release，附带 chart 压缩包 |

发布前更新 [CHANGELOG](../CHANGELOG.md)、[Chart.yaml](../deploy/client-webhook-cert-manager/Chart.yaml) 的版本以及 README 中的安装示例，提交并确认 CI 通过，再创建标签：

```bash
git tag -a v0.1.0 -m "Release v0.1.0"
git push origin v0.1.0
```

工作流会将 `v0.1.0` 转换为镜像标签 `0.1.0`，并以相同版本打包 chart 和 appVersion。带预发布后缀的标签会创建 GitHub prerelease。

发布使用仓库的 `GITHUB_TOKEN`，组织需要允许该仓库创建 GHCR packages。首次发布后分别检查镜像与 chart 包的可见性，以及匿名拉取结果；源码仓库公开不代表包自动公开。参见 [GitHub 包可见性文档](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility)。

本地打包和上传 chart：

```bash
make chart-package VERSION=0.1.0
helm registry login ghcr.io
helm push dist/client-webhook-cert-manager-0.1.0.tgz oci://ghcr.io/console-dns/charts
```

OCI push 地址不包含 chart 名或版本，Helm 会从压缩包元数据中读取。
