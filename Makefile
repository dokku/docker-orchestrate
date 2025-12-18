VERSION ?= 0.2.0

.PHONY: build install test coverage

build:
	go build -o docker-orchestrate -ldflags "-X main.Version=$(VERSION) -X commands.Version=$(VERSION)"

install: build
	mkdir -p ~/.docker/cli-plugins
	cp docker-orchestrate ~/.docker/cli-plugins/docker-orchestrate

test:
	go test -v ./internal/...

coverage:
	go test -coverprofile=coverage.out ./internal/...
	go tool cover -func=coverage.out
	rm coverage.out
