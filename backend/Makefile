.PHONY: build run test lint fmt clean

build:
	go build -o bin/server ./cmd/server

run:
	go run ./cmd/server

test:
	go test ./...

lint:
	go vet ./...
	@which staticcheck > /dev/null && staticcheck ./... || echo "staticcheck not installed, skipping"

fmt:
	gofmt -w .

clean:
	rm -rf bin/
