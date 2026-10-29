.PHONY: build test clean run gen-example gen-all

BINARY_NAME=gowasmrunner

build:
	go build -o bin/$(BINARY_NAME) ./cmd/runner

test:
	go test ./...

gen-example:
	go run examples/gen_add/main.go

gen-all:
	go run examples/gen_add/main.go
	go run examples/gen_greet/main.go
	go run examples/gen_limits/main.go
	go run examples/gen_rust_fibonacci/main.go

run: build gen-example
	./bin/$(BINARY_NAME) -wasm examples/add.wasm -func add 10 20

run-fibonacci: build gen-all
	./bin/$(BINARY_NAME) -wasm examples/rust_fibonacci.wasm -func fibonacci 10

clean:
	rm -rf bin/
	rm -f examples/*.wasm
