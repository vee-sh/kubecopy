BINARY   := kubectl-copy
MODULE   := github.com/a13x22/kube-copy
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS  := -s -w -X main.version=$(VERSION)
GOFLAGS  := -trimpath

PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: build install test clean cross-build lint

build:
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/kubectl-copy

install: build
	@echo "Installing $(BINARY) to /usr/local/bin..."
	cp bin/$(BINARY) /usr/local/bin/$(BINARY)
	@echo "Done. Run 'kubectl copy --help' to verify."

test:
	go test -race -v ./...

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/ dist/

cross-build:
	@mkdir -p dist
	@for platform in $(PLATFORMS); do \
		GOOS=$${platform%%/*} GOARCH=$${platform##*/} \
		CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" \
			-o $(BINARY) ./cmd/kubectl-copy ; \
		tar czf dist/$(BINARY)-$${platform%%/*}-$${platform##*/}.tar.gz $(BINARY) ; \
		shasum -a 256 dist/$(BINARY)-$${platform%%/*}-$${platform##*/}.tar.gz \
			| awk '{print $$1}' > dist/$(BINARY)-$${platform%%/*}-$${platform##*/}.tar.gz.sha256 ; \
		rm -f $(BINARY) ; \
		echo "Built dist/$(BINARY)-$${platform%%/*}-$${platform##*/}.tar.gz"; \
	done

# Generate krew plugin manifest with real sha256 values (run after cross-build).
# Usage: make cross-build && make krew-manifest > plugins/copy.yaml
krew-manifest: dist/$(BINARY)-linux-amd64.tar.gz.sha256 dist/$(BINARY)-linux-arm64.tar.gz.sha256 dist/$(BINARY)-darwin-amd64.tar.gz.sha256 dist/$(BINARY)-darwin-arm64.tar.gz.sha256
	@echo "apiVersion: krew.googlecontainertools.github.com/v1alpha2"
	@echo "kind: Plugin"
	@echo "metadata:"
	@echo "  name: copy"
	@echo "spec:"
	@echo "  version: $(VERSION)"
	@echo "  homepage: https://github.com/a13x22/kube-copy"
	@echo "  shortDescription: Copy Kubernetes resources across namespaces and clusters"
	@echo "  description: |"
	@echo "    Intelligently copies Kubernetes resources, sanitizing metadata and"
	@echo "    detecting conflicts to avoid broken or duplicate resources."
	@echo "    Supports recursive dependency graph traversal."
	@echo "  caveats: |"
	@echo "    This plugin requires read access to the source namespace/cluster and"
	@echo "    create access to the target namespace/cluster."
	@echo "  platforms:"
	@echo "  - selector:"
	@echo "      matchLabels:"
	@echo "        os: linux"
	@echo "        arch: amd64"
	@echo "    bin: kubectl-copy"
	@echo "    uri: https://github.com/a13x22/kube-copy/releases/download/$(VERSION)/kubectl-copy-linux-amd64.tar.gz"
	@echo "    sha256: $$(cat dist/$(BINARY)-linux-amd64.tar.gz.sha256)"
	@echo "  - selector:"
	@echo "      matchLabels:"
	@echo "        os: linux"
	@echo "        arch: arm64"
	@echo "    bin: kubectl-copy"
	@echo "    uri: https://github.com/a13x22/kube-copy/releases/download/$(VERSION)/kubectl-copy-linux-arm64.tar.gz"
	@echo "    sha256: $$(cat dist/$(BINARY)-linux-arm64.tar.gz.sha256)"
	@echo "  - selector:"
	@echo "      matchLabels:"
	@echo "        os: darwin"
	@echo "        arch: amd64"
	@echo "    bin: kubectl-copy"
	@echo "    uri: https://github.com/a13x22/kube-copy/releases/download/$(VERSION)/kubectl-copy-darwin-amd64.tar.gz"
	@echo "    sha256: $$(cat dist/$(BINARY)-darwin-amd64.tar.gz.sha256)"
	@echo "  - selector:"
	@echo "      matchLabels:"
	@echo "        os: darwin"
	@echo "        arch: arm64"
	@echo "    bin: kubectl-copy"
	@echo "    uri: https://github.com/a13x22/kube-copy/releases/download/$(VERSION)/kubectl-copy-darwin-arm64.tar.gz"
	@echo "    sha256: $$(cat dist/$(BINARY)-darwin-arm64.tar.gz.sha256)"
