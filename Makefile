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
	go run ./cmd/gateway -world http://127.0.0.1:8080 -addr :8081

compose-up:
	docker compose up -d

compose-down:
	docker compose down
