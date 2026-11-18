# build tags, comma-separated (make test TAGS=official,slm_purego). "official" is for the
# programs of the workflows of this repository only (TRADEMARKS.md): any other build is an
# unofficial build.
TAGS ?=

all: clean assets build

build:
	GOOS=linux CGO_ENABLED=0 go build -tags "$(TAGS)" -o build/switch-library-manager-web main.go

# binaries used by the Docker image, one per platform
# static programs with the zstd library in C, built by Zig (.github/scripts/setup_zig.sh)
docker-binaries:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=1 CC="zig cc -target x86_64-linux-musl" CXX="zig c++ -target x86_64-linux-musl" go build -tags "$(TAGS)" -trimpath -ldflags="-s -w" -o build/switch-library-manager-web-linux-amd64 main.go
	GOOS=linux GOARCH=arm64 CGO_ENABLED=1 CC="zig cc -target aarch64-linux-musl" CXX="zig c++ -target aarch64-linux-musl" go build -tags "$(TAGS)" -trimpath -ldflags="-s -w" -o build/switch-library-manager-web-linux-arm64 main.go

build-windows:
	GOOS=windows CGO_ENABLED=0 go build -tags "$(TAGS)" -o build/switch-library-manager-web.exe main.go

build-mac:
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -tags "$(TAGS)" -o build/switch-library-manager-web-mac main.go

clean:
	rm -rf build || true
	npm run clean

assets:
	npm run build

run:
	go run -tags "$(TAGS)" main.go

test:
	go vet -tags "$(TAGS)" ./...
	go test -tags "$(TAGS)" ./... ./switchfs/_crypto/

watch:
	npm run watch:css & npm run watch:js

.PHONY: build build-windows build-mac test assets docker-binaries
