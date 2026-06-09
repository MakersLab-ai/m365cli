BINARY := bin/m365
PKG := ./cmd/m365

.PHONY: build test vet fmt check clean

build:
	go build -o $(BINARY) $(PKG)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

check: fmt vet test

clean:
	rm -rf bin
