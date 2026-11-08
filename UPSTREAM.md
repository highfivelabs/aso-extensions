# Upstream

This repository builds on Azure Service Operator v2 (ASO) at one commit of
`main`. `hack/upstream.sh` reads the commit from the table below (the
"Commit" row), fetches it into `third_party/azure-service-operator` and
applies `patches/azure-service-operator/*.patch` in order;
`hack/upstream.sh --check` fails if the checkout is anything else.

| | |
| --- | --- |
| Repository | `github.com/Azure/azure-service-operator` |
| Commit | `b9dcbe48feaf1173f48ab28dd5edc48b514d3f4d` |
| Commit date | 2026-11-06T03:46:05Z ("fix: detect cycles in owner hierarchy resolution (#5825)") |
| Branch | `main` |
| Nearest release | `v2.22.0` (2026-10-30); the pin is 16 commits ahead of it |
| Pinned on | 2026-11-07 |
| How | `git fetch --depth 1 origin <commit>`; used through `replace` in `go.mod`, never vendored or committed |
