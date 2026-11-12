#!/usr/bin/env bash
set -euo pipefail

chart_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../deploy/client-webhook-cert-manager" && pwd)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT

rendered="${tmp_dir}/default.yaml"
overridden="${tmp_dir}/overridden.yaml"
helm template smoke "${chart_dir}" --namespace cert-manager >"${rendered}"

require() {
  local needle=$1
  local file=$2
  if ! grep -Fq -- "${needle}" "${file}"; then
    echo "chart smoke test: missing '${needle}' in ${file}" >&2
    exit 1
  fi
}

require 'kind: APIService' "${rendered}"
require 'cert-manager.io/inject-ca-from: "cert-manager/smoke-client-webhook-cert-manager-serving-cert"' "${rendered}"
require '  - console-dns-api-token' "${rendered}"
require 'verbs: ["get"]' "${rendered}"
require 'name: smoke-client-webhook-cert-manager-serving-cert' "${rendered}"
require 'runAsUser: 65532' "${rendered}"

helm template smoke "${chart_dir}" \
  --namespace webhook-system \
  --set dnsAPI.caBundleSecretRef.name=console-dns-ca \
  --set dnsAPI.caBundleSecretRef.key=ca.pem \
  --set-json 'secretAccess=[{"namespace":"cert-manager","secretNames":["console-dns-api-token"]},{"namespace":"tenant-a","secretNames":["tenant-api-token","tenant-api-token-2"]}]' \
  --set image.digest=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
  >"${overridden}"

require 'secretName: console-dns-ca' "${overridden}"
require 'value: /etc/console-dns/ca-bundle/ca.pem' "${overridden}"
require 'name: smoke-client-webhook-cert-manager-secret-1' "${overridden}"
require 'namespace: tenant-a' "${overridden}"
require 'resourceNames:' "${overridden}"
require 'image: ghcr.io/console-dns/client-webhook-cert-manager@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' "${overridden}"

if helm template smoke "${chart_dir}" --namespace webhook-system \
  --set dnsAPI.insecureSkipTLSVerify=true \
  --set dnsAPI.caBundleSecretRef.name=console-dns-ca >/dev/null 2>&1; then
  echo "chart smoke test: insecure TLS verification and custom CA must be rejected" >&2
  exit 1
fi

helm template smoke "${chart_dir}" --namespace webhook-system \
  --set dnsAPI.insecureSkipTLSVerify=true >"${tmp_dir}/skip-tls.yaml"
require 'value: "true"' "${tmp_dir}/skip-tls.yaml"

# Truncation must preserve the suffixes that distinguish CA and serving resources.
long_name="$(printf 'x%.0s' {1..63})"
helm template smoke "${chart_dir}" --namespace cert-manager \
  --set fullnameOverride="${long_name}" >"${tmp_dir}/long-name.yaml"
require "  name: ${long_name:0:60}-ca" "${tmp_dir}/long-name.yaml"
require "  name: ${long_name:0:52}-selfsigned" "${tmp_dir}/long-name.yaml"
require "  name: ${long_name:0:50}-serving-cert" "${tmp_dir}/long-name.yaml"
awk '
  /^---/ { kind = ""; metadata = 0 }
  /^kind:/ { kind = $2 }
  /^metadata:/ { metadata = 1; next }
  metadata && /^  name:/ {
    if (length($2) > 63) {
      print "chart smoke test: resource name exceeds 63 characters: " $2 > "/dev/stderr"
      exit 1
    }
    if (kind == "Certificate" || kind == "Issuer") {
      if (seen[kind SUBSEP $2]++) {
        print "chart smoke test: duplicate " kind " name: " $2 > "/dev/stderr"
        exit 1
      }
    }
    metadata = 0
  }
' "${tmp_dir}/long-name.yaml"

echo "chart smoke tests passed"
