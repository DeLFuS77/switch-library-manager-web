#!/usr/bin/env bash
# Installs Zig, the C compiler of the programs (cgo builds the zstd library in C with it, for
# every system from one Linux machine), checked against the SHA-256 published by ziglang.org,
# and puts it on the PATH of the next steps. A new version is taken on purpose, by changing
# both lines below.
set -euo pipefail

version="0.16.0"
sha256="70e49664a74374b48b51e6f3fdfbf437f6395d42509050588bd49abe52ba3d00"

folder="${RUNNER_TEMP:-/tmp}/zig-${version}"
archive="${folder}.tar.xz"
curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
	-o "${archive}" "https://ziglang.org/download/${version}/zig-x86_64-linux-${version}.tar.xz"
echo "${sha256}  ${archive}" | sha256sum --check --strict
mkdir -p "${folder}"
tar -xJf "${archive}" -C "${folder}" --strip-components 1
rm -f "${archive}"
"${folder}/zig" version
if [ -n "${GITHUB_PATH:-}" ]; then
	echo "${folder}" >> "${GITHUB_PATH}"
fi