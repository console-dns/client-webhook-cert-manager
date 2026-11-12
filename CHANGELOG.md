# Changelog

## [0.1.0] - 2026-11-13

首次发布 Console DNS 的 cert-manager DNS01 webhook。

### Added

- 基于固定版本 `github.com/console-dns/client v0.0.0-20250418013039-135fd950cbe2` 的 TXT 创建与清理。
- 幂等 Present/CleanUp、同名多验证值共存、重复记录清理和 CNAME 委派目标支持。
- Kubernetes Secret 凭据引用、token 轮换及按 Secret 名称限制的 RBAC。
- DNS API 请求超时、自定义 CA、可选跳过 TLS 证书校验及重定向限制。
- 包含 APIService、服务证书和 CA 注入的 OCI Helm chart。
- Linux amd64/arm64 非 root 容器镜像构建与 GHCR 发布工作流。
- Go race/契约测试、cert-manager 官方 envtest 集成测试和 Helm 模板检查。
- 安装配置示例、部署说明和开发文档。

[0.1.0]: https://github.com/console-dns/client-webhook-cert-manager/releases/tag/v0.1.0
