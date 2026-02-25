BINARY ?= tcsss
GO ?= go
GOTEST ?= $(GO) test
GOFMT ?= gofmt
VERSION ?= dev

PKG := tcsss/internal/version
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE := $(shell TZ=Asia/Shanghai date '+%Y-%m-%dT%H:%M:%S%:z')
LDFLAGS := -s -w \
	-X '$(PKG).Version=$(VERSION)' \
	-X '$(PKG).Commit=$(COMMIT)' \
	-X '$(PKG).BuildTime=$(DATE)'

build:
	GOOS=linux GOARCH=amd64 $(GO) build -ldflags="$(LDFLAGS)" -o $(BINARY) ./cmd/tcsss

build-arm64:
	GOOS=linux GOARCH=arm64 $(GO) build -ldflags="$(LDFLAGS)" -o $(BINARY)-arm64 ./cmd/tcsss

version:
	@echo $(VERSION)

test:
	$(GOTEST) -v ./...

fmt:
	$(GOFMT) -w cmd internal

tidy:
	$(GO) mod tidy

clean:
	rm -f $(BINARY) $(BINARY)-arm64 coverage.out coverage.html quality-report.txt
	rm -rf internal/*/mocks

.PHONY: build build-arm64 test fmt tidy clean version
