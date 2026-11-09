# Copyright 2026 High Five Labs
# SPDX-License-Identifier: MIT
#
# Every target needs the pinned Azure Service Operator checkout;
# `make upstream` fetches it (about 675 MB, not committed).

# The project's name: the image, and the prefix of the install's objects.
NAME := aso-extensions

export GOTOOLCHAIN ?= local

BIN := $(CURDIR)/bin
ENVTEST_K8S_VERSION := 1.37.0
# setup-envtest at controller-runtime's version in ASO's go.mod.
SETUP_ENVTEST := sigs.k8s.io/controller-runtime/tools/setup-envtest@v0.25.2

.PHONY: all upstream upstream-check generate generate-arm build test check operator-e2e clean

all: check

upstream:
	hack/upstream.sh

upstream-check:
	hack/upstream.sh --check
	hack/check-go-mod.sh

generate: upstream-check
	hack/generate.sh

# ASO's generator over the ARM specs (heavy: run it on the Ubuntu machine).
generate-arm: upstream-check
	hack/generate-arm.sh
	hack/generate.sh

build: upstream-check
	go build -o $(BIN)/manager ./cmd/manager

$(BIN)/setup-envtest:
	GOBIN=$(BIN) go install $(SETUP_ENVTEST)

# Unit tests, and the end-to-end tests (test/e2e): envtest, a fake Entra and
# a fake Key Vault in process, and PostgreSQL in Docker.
test: upstream-check $(BIN)/setup-envtest
	KUBEBUILDER_ASSETS="$$($(BIN)/setup-envtest use $(ENVTEST_K8S_VERSION) --bin-dir $(BIN)/envtest -p path)" \
		go test ./... -count=1

# Everything that must hold before a commit.
check: upstream-check
	@test -z "$$(gofmt -l apis cmd pkg test)" || { gofmt -l apis cmd pkg test; echo "gofmt needed"; exit 1; }
	hack/generate-arm.sh
	hack/generate.sh
	@git diff --exit-code -- apis config pkg/controllers generator || { echo "generated files are stale; commit the output of make generate-arm"; exit 1; }
	go vet ./...
	go vet -tags fakecloud ./cmd/manager
	hack/check-no-fake-cloud.sh
	hack/check-spdx.sh
	$(MAKE) build test

# The install on kind with the fake cloud (tests/operator/run.sh).
operator-e2e: upstream-check
	NAME=$(NAME) tests/operator/run.sh

clean:
	rm -rf bin
