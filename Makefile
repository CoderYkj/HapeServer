.PHONY: build test race lint tidy run-world run-gateway compose-up compose-down

build:
	go build ./...

test:
	go test ./...

race:
	go test -race ./...

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

run-world:
	go run ./cmd/world -config configs/default.json

run-gateway:
	go run ./cmd/gateway -config configs/default.json

compose-up:
	docker compose up -d

compose-down:
	docker compose down
