IMAGE ?= ghcr.io/weisshorn-cyd/twinward:latest

.PHONY: lint
lint: lint-go lint-yaml

.PHONY: lint-go
lint-go:
	golangci-lint run ./...

.PHONY: lint-yaml
lint-yaml:
	yamllint .

.PHONY: test
test:
	go test ./...

.PHONY: build
build:
	go build -o bin/twinward ./cmd/twinward

.PHONY: docker-build
docker-build:
	docker build -t $(IMAGE) .

.PHONY: install
install:
	kubectl apply -f config/crd.yaml
	kubectl apply -f config/manifests.yaml
