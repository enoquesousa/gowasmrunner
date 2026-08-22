# Contributing to gowasmrunner

First off, thank you for considering contributing to `gowasmrunner`! It's people like you that make open source such a great community.

## Development Setup

1. Ensure you have **Go 1.21 or newer** installed.
2. Clone the repository: `git clone https://github.com/enoquesousa/gowasmrunner.git`
3. Download dependencies: `go mod download`
4. Run the initial tests to ensure your environment is sane: `make test`

## Code Style and Conventions

- We adhere strictly to standard Go idioms. Read [Effective Go](https://golang.org/doc/effective_go.html) and [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments).
- **Run `go fmt`** on your code before committing.
- Ensure every exported type, interface, and function is properly documented with Godoc standard comments.

## Pull Request Process

1. **Branch Naming**: Use descriptive branch names like `feat/json-support` or `fix/memory-leak`.
2. **Commit Messages**: We prefer [Conventional Commits](https://www.conventionalcommits.org/). Example: `feat: add filesystem watcher for plugins`.
3. **Tests**: Any new feature or bug fix must include relevant integration or unit tests in the `tests/` directory.
4. **Review**: Open your PR and request a review. Be prepared to discuss your architectural choices.

## Areas Open for Contribution

- **JSON Support**: Helping the host and guest communicate via serialized JSON objects safely.
- **Hot-Reloading**: Automatically reloading cached plugins if the underlying `.wasm` file changes.
- **WASI Extensions**: Expanding filesystem or networking access via WASI in a strictly scoped manner.
