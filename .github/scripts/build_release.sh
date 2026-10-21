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

# The zstd library in C (several threads on one stream for the balanced and max levels) is
# built with cgo and Zig (.github/scripts/setup_zig.sh), for every system from this machine:
# the programs of Linux are static (musl), and run on any Linux. macOS needs the libraries of
# Apple, which only a Mac has: its programs stay in Go only, as before ("-").
#
# system  GOOS     GOARCH  GOARM  Zig target
targets=(
	"windows-x64    windows amd64 -  x86_64-windows-gnu"
	"windows-arm64  windows arm64 -  aarch64-windows-gnu"
	"macos-apple    darwin  arm64 -  -"
	"macos-intel    darwin  amd64 -  -"
	"linux-x64      linux   amd64 -  x86_64-linux-musl"
	"linux-arm64    linux   arm64 -  aarch64-linux-musl"
	"linux-armv7    linux   arm   7  arm-linux-musleabihf"
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
	read -r system goos goarch goarm zigtarget <<<"${target}"
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
	if [ "${zigtarget}" = "-" ]; then
		extra+=("CGO_ENABLED=0")
	else
		extra+=("CGO_ENABLED=1" "CC=zig cc -target ${zigtarget}" "CXX=zig c++ -target ${zigtarget}")
	fi
	env GOOS="${goos}" GOARCH="${goarch}" "${extra[@]}" \
		go build -trimpath -ldflags="-s -w" -o "${folder}/${binary}" .
	# a program meant to have the zstd library in C has it, and one of Linux needs no library
	# of the system: else the release stops here instead of shipping a program that is slower
	# or does not start on some systems
	if [ "${zigtarget}" != "-" ] && ! go version -m "${folder}/${binary}" | grep -q "CGO_ENABLED=1"; then
		echo "${system} was built without the zstd library in C" >&2
		exit 1
	fi
	if [ "${goos}" = "linux" ] && readelf -l "${folder}/${binary}" | grep -q "program interpreter"; then
		echo "${system} is not static" >&2
		exit 1
	fi
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
