all: clean assets build

build:
	GOOS=linux CGO_ENABLED=0 go build -o build/switch-library-manager-web main.go

build-windows:
	GOOS=windows CGO_ENABLED=0 go build -o build/switch-library-manager-web.exe main.go

build-mac:
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o build/switch-library-manager-web-mac main.go

clean:
	rm -rf build || true
	npm run clean

assets:
	npm run build

run:
	go run main.go

test:
	go vet ./...
	go test ./... ./switchfs/_crypto/

watch:
	npm run watch:css & npm run watch:js

.PHONY: build build-windows build-mac test assets
