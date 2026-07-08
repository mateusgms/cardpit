VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GOFLAGS  = -trimpath
LDFLAGS  = -s -w -X main.version=$(VERSION)
ZIPNAME  = cardpit-$(VERSION)-windows-amd64.zip

.PHONY: all build release web dev check test fmt vet buildwin checksums zip clean

all: build

## build: build a native binary (for local dev on Linux/macOS)
build:
	cd core && go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o ../dist/cardpit ./cmd/cardpit

## release: build the single-file Windows executable (UI embedded)
release: web
	mkdir -p dist
	cd core && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
		go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o ../dist/cardpit.exe ./cmd/cardpit

## checksums: generate SHA-256 checksum file for the Windows exe
checksums: release
	cd dist && sha256sum cardpit.exe > checksums.txt

## zip: create the all-in-one installer zip for distribution
zip: checksums
	cp packaging/config-release.yaml dist/config.yaml
	cp packaging/setup.bat dist/setup.bat
	cp packaging/INSTALL.md dist/INSTALL.md
	cd dist && zip -j $(ZIPNAME) cardpit.exe checksums.txt config.yaml setup.bat INSTALL.md

## web: build the React UI and stage it for go:embed
web:
	cd web && npm ci && npm run build
	rm -rf core/internal/httpapi/webui/dist
	cp -r web/dist core/internal/httpapi/webui/dist

## dev: run the service on the fake platform (Linux dev loop)
dev:
	cd core && go run ./cmd/cardpit run --config ../config.dev.yaml

## check: everything CI would run
check: fmt vet test buildwin

fmt:
	@out=$$(gofmt -l core); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet:
	cd core && go vet ./...

test:
	cd core && go test -race ./...

## buildwin: prove the Windows target compiles (incl. platform/win)
buildwin:
	cd core && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...

clean:
	rm -rf dist
