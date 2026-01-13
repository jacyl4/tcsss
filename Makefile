BINARY ?= tcsss
GO ?= go
GOTEST ?= $(GO) test
GOFMT ?= gofmt

build:
	GOOS=linux GOARCH=amd64 $(GO) build -ldflags="-s -w" -o $(BINARY) ./cmd/tcsss

build-arm64:
	GOOS=linux GOARCH=arm64 $(GO) build -ldflags="-s -w" -o $(BINARY)-arm64 ./cmd/tcsss

test:
	$(GOTEST) -v ./...

fmt:
	$(GOFMT) -w cmd internal

tidy:
	$(GO) mod tidy

clean:
	rm -f $(BINARY) $(BINARY)-arm64 coverage.out coverage.html quality-report.txt
	rm -rf internal/*/mocks

