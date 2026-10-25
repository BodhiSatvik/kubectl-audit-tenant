#!/usr/bin/env bash
# Puts the pinned krew release on PATH as kubectl-krew, checked against its sha256 (D188, D189).
# It runs the checked binary itself: `krew install krew` would fetch another krew from the live index.
# In a GitHub Actions step it adds the folder to $GITHUB_PATH for the next steps; elsewhere it prints it.
set -euo pipefail
version=v0.5.0
os=$(uname | tr '[:upper:]' '[:lower:]')
arch=$(uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')
# sha256 of krew-<os>_<arch>.tar.gz; the krew release's own .sha256 files and GitHub's asset digests agreed on 2026-10-25.
case "${os}_${arch}" in
  linux_amd64) sum=5d5a221fffdf331d1c5c68d9917530ecd102e0def5b5a6d62eeed1c404efb28a ;;
  darwin_arm64) sum=cd6e58b4e954e301abd19001d772846997216d696bcaa58f0bcf04708339ece3 ;;
  *) echo "install-krew: no pinned sha256 for ${os}_${arch}; add it from the krew ${version} release" >&2; exit 1 ;;
esac
dir=$(mktemp -d)
cd "$dir"
curl -fsSL --retry 3 -O "https://github.com/kubernetes-sigs/krew/releases/download/${version}/krew-${os}_${arch}.tar.gz"
echo "${sum}  krew-${os}_${arch}.tar.gz" | shasum -a 256 -c - >&2 # stdout carries only the folder
tar zxf "krew-${os}_${arch}.tar.gz"
mv "krew-${os}_${arch}" kubectl-krew
if [ -n "${GITHUB_PATH:-}" ]; then echo "$dir" >> "$GITHUB_PATH"; else echo "$dir"; fi
