IMAGE ?= ghcr.io/weisshorn-cyd/twinward:latest

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
