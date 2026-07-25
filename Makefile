.PHONY: build test run run-worker fmt vet compose-up compose-down

GOCACHE := /tmp/heatcheck-go-cache
GOMODCACHE := /tmp/heatcheck-go-mod

build:
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) go build -buildvcs=false -o bin/heatcheck-api ./cmd/api
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) go build -buildvcs=false -o bin/heatcheck-worker ./cmd/worker
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) go build -buildvcs=false -o bin/heatcheck-admin ./cmd/admin

test:
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) go test ./...

run:
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) go run -buildvcs=false ./cmd/api

run-worker:
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) go run -buildvcs=false ./cmd/worker

fmt:
	gofmt -w cmd internal

vet:
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) go vet ./...

compose-up:
	docker compose up --build

compose-down:
	docker compose down
