#!/usr/bin/env bash
# Builds the program for every system, or for the systems given, and packs each one with the
# README, the license and the notice: dist/switch-library-manager-web-<system>.zip (Windows) or
# .tar.gz, holding the folder switch-library-manager-web. No name has the version: the app keeps
# its data next to itself, and a new version unpacked over the folder of the one before replaces
# the program and keeps the data, instead of starting without it in a folder of its own. The web
# assets must be built first (npm run build).
#
# Usage: .github/scripts/build_release.sh 1.24.0 [system...]
set -euo pipefail

version="${1:?version}"
shift
wanted=" $* "
name="switch-library-manager-web"
root="$(pwd)"
rm -rf dist
mkdir -p dist

# The zstd library in C (several threads on one stream for the balanced and max levels) is
# built with cgo and Zig (.github/scripts/setup_zig.sh), for Windows and Linux from one Linux
# machine: the programs of Linux are static (musl), and run on any Linux. macOS needs the
# libraries of Apple, which only a Mac has: its programs are built on a Mac of each kind, with
# its own C compiler ("mac"), as the release does.
#
# system  GOOS     GOARCH  GOARM  Zig target
targets=(
	"windows-x64    windows amd64 -  x86_64-windows-gnu"
	"windows-arm64  windows arm64 -  aarch64-windows-gnu"
	"macos-apple    darwin  arm64 -  mac"
	"macos-intel    darwin  amd64 -  mac"
	"linux-x64      linux   amd64 -  x86_64-linux-musl"
	"linux-arm64    linux   arm64 -  aarch64-linux-musl"
	"linux-armv7    linux   arm   7  arm-linux-musleabihf"
)

# with systems given, only those; without, every one but macOS, which needs a Mac
chosen() {
	if [ "${wanted}" != "  " ]; then
		[[ "${wanted}" == *" $1 "* ]]
	else
		[[ "$1" != macos-* ]]
	fi
}

# the Windows program gets its icon and version details (go-winres writes .syso files that
# go build picks up for Windows only)
if chosen windows-x64 || chosen windows-arm64; then
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
fi

for target in "${targets[@]}"; do
	read -r system goos goarch goarm zigtarget <<<"${target}"
	if ! chosen "${system}"; then
		continue
	fi
	stage="dist/${system}"
	folder="${stage}/${name}"
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
	if [ "${zigtarget}" = "mac" ]; then
		# the C compiler of the Mac, for the oldest macOS Go supports
		extra+=("CGO_ENABLED=1" "MACOSX_DEPLOYMENT_TARGET=13.0")
	else
		extra+=("CGO_ENABLED=1" "CC=zig cc -target ${zigtarget}" "CXX=zig c++ -target ${zigtarget}")
	fi
	env GOOS="${goos}" GOARCH="${goarch}" "${extra[@]}" \
		go build -trimpath -ldflags="-s -w" -o "${folder}/${binary}" .
	# a program meant to have the zstd library in C has it, and one of Linux needs no library
	# of the system: else the release stops here instead of shipping a program that is slower
	# or does not start on some systems
	if ! go version -m "${folder}/${binary}" | grep -q "CGO_ENABLED=1"; then
		echo "${system} was built without the zstd library in C" >&2
		exit 1
	fi
	if [ "${goos}" = "linux" ] && readelf -l "${folder}/${binary}" | grep -q "program interpreter"; then
		echo "${system} is not static" >&2
		exit 1
	fi
	cp README.md LICENSE NOTICE.md CHANGELOG.md "${folder}/"
	(
		cd "${stage}"
		if [ "${goos}" = "windows" ]; then
			zip -qr "../${name}-${system}.zip" "${name}"
		else
			chmod +x "${name}/${binary}"
			tar -czf "../${name}-${system}.tar.gz" "${name}"
		fi
	)
	rm -rf "${stage}"
done

rm -f rsrc_windows_*.syso
ls -l dist
cd "${root}"
