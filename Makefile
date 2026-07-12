MAKEFLAGS += --silent

## build: Compile ./bin/hadi.
build:
	@go build -ldflags "-X main.version=$$(git describe --tags 2>/dev/null || echo dev)" -o bin/hadi .

## build-linux: Static linux/amd64 binary (release artifact).
build-linux:
	@CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-X main.version=$$(git describe --tags 2>/dev/null || echo dev)" -o bin/hadi-linux-amd64 .

## test: Run the suite.
test:
	@go vet ./... && go test ./...

## clean: Remove build output.
clean:
	@rm -rf bin

.PHONY: build build-linux test clean help
all: help
help: Makefile
	@echo
	@sed -n 's/^##//p' $< | column -t -s ':' | sed -e 's/^/ /'
	@echo
