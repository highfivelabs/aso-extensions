#!/usr/bin/env bash
# Copyright 2026 High Five Labs
# SPDX-License-Identifier: Apache-2.0
# .gitattributes marks the generated files linguist-generated, so GitHub
# collapses them and factory/review leaves them out (HFL-810). This holds
# both directions: every committed file whose header says a generator
# wrote it ("DO NOT EDIT" in its first 15 lines) is marked, and no file
# under the hand-written paths is.
set -o errexit -o nounset -o pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

# The marked files: git check-attr -z prints path, attribute and value,
# each NUL-ended.
git ls-files -z | git check-attr -z --stdin linguist-generated | tr '\0' '\n' |
  paste - - - | awk -F'\t' '$3 != "unspecified" && $3 != "unset" && $3 != "false" {print $1}' |
  LC_ALL=C sort >"${tmp}/marked"

# The files with a generator's header: git grep finds the candidates, and
# the header (the first 15 lines) decides.
git grep -z -l -I 'DO NOT EDIT' -- ':!:.gitattributes' ':!:.review-generated' \
  ':!:hack/check-generated-attrs.sh' ':!:third_party' |
  xargs -0 awk 'FNR <= 15 && /DO NOT EDIT/ {print FILENAME; nextfile}' |
  LC_ALL=C sort -u >"${tmp}/headers"

bad=0
while IFS= read -r f; do
  echo "${f}: has a generator's header but .gitattributes does not mark it linguist-generated" >&2
  bad=1
done < <(LC_ALL=C comm -23 "${tmp}/headers" "${tmp}/marked")

# Hand-written paths are never marked.
while IFS= read -r f; do
  case "${f}" in
    generator/_extensions/*|pkg/fakecloud/*|test/*|tests/*|cmd/*|patches/*|hack/*.sh|hack/coverage/*|docs/coverage/README.md|README.md)
      echo "${f}: hand-written, but .gitattributes marks it linguist-generated" >&2
      bad=1 ;;
  esac
done <"${tmp}/marked"

exit "${bad}"
