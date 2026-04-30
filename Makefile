BINARY_NAME := turnitoffandonagain
GO          := go

.PHONY: build test lint clean

build:
	$(GO) build -o $(BINARY_NAME) .

test:
	$(GO) test ./...

lint:
	golangci-lint run

clean:
	rm -f $(BINARY_NAME)
