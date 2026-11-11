#!/usr/bin/env bash
# Copyright 2026 High Five Labs
# SPDX-License-Identifier: MIT
#
# task release TAG=v0.1.0 (release.yml's images job): build the manager
# image with ko for linux/amd64 and linux/arm64, push it to KO_DOCKER_REPO
# tagged TAG, and write the resolved install (config/default with that
# image) to dist/aso-extensions-TAG.yaml.
#
# Pushing needs write access to the registry: release.yml logs ko in with
# a Workload Identity token (REGISTRY_TOKEN); by hand, a ko or docker
# login you already have. go.mod points into the upstream checkout: run
# task upstream first. Environment: TAG, KO_DOCKER_REPO, REGISTRY,
# REGISTRY_TOKEN (optional), GITHUB_STEP_SUMMARY (optional).
set -euo pipefail

: "${TAG:?TAG is the version tag}" "${KO_DOCKER_REPO:?}" "${REGISTRY:?}"
if [[ -n "${REGISTRY_TOKEN:-}" ]]; then
  ko login "${REGISTRY}" --username oauth2accesstoken --password-stdin <<<"${REGISTRY_TOKEN}"
fi

mkdir -p dist
manifest="dist/aso-extensions-${TAG}.yaml"
kubectl kustomize config/default >dist/kustomized.yaml
ko resolve --base-import-paths --tags "${TAG}" --platform linux/amd64,linux/arm64 \
  -f dist/kustomized.yaml >"${manifest}"
rm -f dist/kustomized.yaml
grep -E 'image: ' "${manifest}" | sort -u | tee -a "${GITHUB_STEP_SUMMARY:-/dev/null}"
