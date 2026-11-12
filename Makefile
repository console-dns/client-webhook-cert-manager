SHELL := /bin/bash

MODULE := github.com/console-dns/client-webhook-cert-manager
CHART_DIR := deploy/client-webhook-cert-manager
IMAGE ?= ghcr.io/console-dns/client-webhook-cert-manager
VERSION ?= $(shell awk '/^appVersion:/ {gsub(/"/,"",$$2); print $$2}' $(CHART_DIR)/Chart.yaml)

.PHONY: test vet build helm-lint helm-test integration-test chart-package image

test:
	go test -race ./...

vet:
	go vet ./...

build:
	mkdir -p bin
	go build -o bin/webhook ./cmd/webhook

helm-lint:
	helm lint $(CHART_DIR)

helm-test: helm-lint
	./scripts/chart-smoke-test.sh

chart-package:
	mkdir -p dist
	helm package $(CHART_DIR) --destination dist --version $(VERSION) --app-version $(VERSION)

image:
	docker build --build-arg VERSION=$(VERSION) --tag $(IMAGE):$(VERSION) .

integration-test:
	@test -n "$${TEST_ASSET_ETCD:-}" || (echo 'TEST_ASSET_ETCD must point to the envtest etcd binary' >&2; exit 1)
	@test -n "$${TEST_ASSET_KUBE_APISERVER:-}" || (echo 'TEST_ASSET_KUBE_APISERVER must point to the envtest kube-apiserver binary' >&2; exit 1)
	@test -n "$${TEST_ASSET_KUBECTL:-}" || (echo 'TEST_ASSET_KUBECTL must point to the envtest kubectl binary' >&2; exit 1)
	go test -race -tags=integration ./tests/integration/...
