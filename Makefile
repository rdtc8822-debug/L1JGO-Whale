.PHONY: build run clean tidy

BINARY=l1jgo

build:
	go build -o bin/$(BINARY) ./cmd/l1jgo

run: build
	./bin/$(BINARY)

clean:
	rm -rf bin/

tidy:
	go mod tidy
