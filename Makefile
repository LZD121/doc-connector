.PHONY: build test vet race lint snapshot clean

build:
	go build ./...

test:
	go test ./...

vet:
	go vet ./...

race:
	go test -race ./...

lint:
	golangci-lint run

snapshot:
	goreleaser release --snapshot --clean

clean:
	rm -rf dist doc-connector
