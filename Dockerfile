# Build Stage
FROM golang:1.26.10-alpine AS builder

ENV GOTOOLCHAIN=local

WORKDIR /app

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code
COPY . .

# Compile the static binary
RUN CGO_ENABLED=0 GOOS=linux go build -o gowasmrunner ./cmd/runner

# Generate example plugins
RUN mkdir -p /app/plugins
RUN go build -o gen_greet examples/gen_greet/main.go && ./gen_greet && mv examples/greet.wasm /app/plugins/plugin-greet.wasm
RUN go build -o gen_add examples/gen_add/main.go && ./gen_add && mv examples/add.wasm /app/plugins/plugin-add.wasm

# Run Stage
FROM alpine:3.24.3

WORKDIR /root/

# Copy the binary
COPY --from=builder /app/gowasmrunner .

# Copy the compiled plugins
COPY --from=builder /app/plugins ./plugins

EXPOSE 8080

# Start the HTTP server by default
ENTRYPOINT ["./gowasmrunner"]
CMD ["-mode", "server", "-host", "0.0.0.0", "-port", "8080", "-plugins", "./plugins", "-tls-cert", "/certs/server.crt", "-tls-key", "/certs/server.key"]
