#!/usr/bin/env bash
# Builds the program for every system and packs each one with the README, the license and
# the notice: dist/switch-library-manager-web-<version>-<system>.zip (Windows) or .tar.gz,
# and SHA256SUMS.txt. The web assets must be built first (npm run build).
#
# Usage: .github/scripts/build_release.sh 1.24.0
set -euo pipefail

version="${1:?version}"
name="switch-library-manager-web"
root="$(pwd)"
rm -rf dist
mkdir -p dist

# system  GOOS     GOARCH  GOARM
targets=(
	"windows-x64    windows amd64 -"
	"windows-arm64  windows arm64 -"
	"macos-apple    darwin  arm64 -"
	"macos-intel    darwin  amd64 -"
	"linux-x64      linux   amd64 -"
	"linux-arm64    linux   arm64 -"
	"linux-armv7    linux   arm   7"
)

# the Windows program gets its icon and version details (go-winres writes .syso files that
# go build picks up for Windows only)
go run github.com/tc-hib/go-winres@v0.3.3 simply \
	--icon resources/static/android-chrome-512x512.png \
	--arch amd64,arm64 \
	--manifest cli \
	--product-name "Switch Library Manager Web" \
	--file-description "Switch Library Manager Web" \
	--product-version "${version}" \
	--file-version "${version}" \
	--copyright "DeLFuS77 and contributors" \
	--original-filename "${name}.exe"

for target in "${targets[@]}"; do
	read -r system goos goarch goarm <<<"${target}"
	folder="dist/${name}-${version}-${system}"
	mkdir -p "${folder}"
	binary="${name}"
	if [ "${goos}" = "windows" ]; then
		binary="${name}.exe"
	fi
	echo "Building ${system}"
	extra=()
	if [ "${goarm}" != "-" ]; then
		extra=("GOARM=${goarm}")
	fi
	env GOOS="${goos}" GOARCH="${goarch}" CGO_ENABLED=0 "${extra[@]}" \
		go build -trimpath -ldflags="-s -w" -o "${folder}/${binary}" .
	cp README.md LICENSE NOTICE.md CHANGELOG.md "${folder}/"
	(
		cd dist
		if [ "${goos}" = "windows" ]; then
			zip -qr "${name}-${version}-${system}.zip" "${name}-${version}-${system}"
		else
			chmod +x "${name}-${version}-${system}/${binary}"
			tar -czf "${name}-${version}-${system}.tar.gz" "${name}-${version}-${system}"
		fi
	)
	rm -rf "${folder}"
done

rm -f rsrc_windows_*.syso
(cd dist && sha256sum -- *.zip *.tar.gz > SHA256SUMS.txt)
ls -l dist
cd "${root}"
