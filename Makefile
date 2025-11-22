BINARY ?= tcsss
VERSION := 1.0.1
LDFLAGS := -ldflags "-s -w -X 'tcsss/internal/version.Current=$(VERSION)'"

build:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY) ./cmd/tcsss

build-arm64:
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY)-arm64 ./cmd/tcsss

test:
	go test -v ./...

fmt:
	gofmt -w cmd internal

tidy:
	go mod tidy

clean:
	rm -f $(BINARY) $(BINARY)-arm64 coverage.out coverage.html quality-report.txt
	rm -rf internal/*/mocks

