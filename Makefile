BINARY ?= cluster-autoheal
IMAGE ?= ghcr.io/vultr/cluster-autoheal
TAG ?= dev
PLATFORM ?= linux/amd64
VERSION ?= $(TAG)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

.PHONY: build
build:
	mkdir -p bin
	go build -trimpath -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)" -o bin/$(BINARY) ./cmd/cluster-autoheal

.PHONY: build-linux
build-linux:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)" -o bin/$(BINARY)-linux-amd64 ./cmd/cluster-autoheal

.PHONY: test
test:
	go test ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: fmt
fmt:
	gofmt -w cmd internal

.PHONY: fmt-check
fmt-check:
	test -z "$$(gofmt -l cmd internal)"

.PHONY: lint
lint: fmt-check vet

.PHONY: image
image: build-linux
	docker buildx build \
		--platform $(PLATFORM) \
		--load \
		-t $(IMAGE):$(TAG) .

.PHONY: image-push
image-push: build-linux
	docker buildx build \
		--platform $(PLATFORM) \
		--push \
		-t $(IMAGE):$(TAG) .

.PHONY: helm-lint
helm-lint:
	helm lint charts/cluster-autoheal

.PHONY: helm-template
helm-template:
	helm template cluster-autoheal charts/cluster-autoheal
