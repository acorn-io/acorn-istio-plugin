build:
	CGO_ENABLED=0 go build -o bin/istio-plugin -ldflags "-s -w" .

tidy:
	go mod tidy

lint: setup-env
	golangci-lint --timeout 5m run

generate:
	go generate

test:
	go test ./...

validate: tidy lint
	if [ -n "$$(git status --porcelain)" ]; then \
		git status --porcelain; \
		echo "Encountered dirty repo!"; \
		exit 1 \
	;fi

GOLANGCI_LINT_VERSION ?= v1.51.1
setup-env: 
	if ! command -v golangci-lint &> /dev/null; then \
  		echo "Could not find golangci-lint, installing version $(GOLANGCI_LINT_VERSION)."; \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $$(go env GOPATH)/bin $(GOLANGCI_LINT_VERSION); \
	fi

image:
	docker build .

goreleaser:
	goreleaser build --snapshot --single-target --rm-dist

