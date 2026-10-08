.PHONY: build test lint check generate

build:
	go build -trimpath -o bin/rigger ./cmd/rigger

test:
	go test ./...

lint:
	golangci-lint run ./...

generate:
	go generate ./...

check: lint test
	shellcheck install.sh
