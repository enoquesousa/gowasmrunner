# Architecture and Design Decisions (ADR)

This document summarizes the core architectural choices of the `gowasmrunner` project.

## 1. Overview

`gowasmrunner` was designed as a hybrid orchestrator (CLI and HTTP Server) focused on the **isolated execution** of WebAssembly binary code.

The ecosystem is split into four main layers:
1. **Entry Interface (`cmd/runner`)**: Handles command interpretation, flag parsing, spinning up the web server, and dependency injection.
2. **HTTP Gateway (`internal/gateway`)**: Validates the listener/TLS configuration and API-token policy, rejects unauthorized requests before reading bodies, and limits HTTP input.
3. **Wasm Engine (`internal/engine`)**: Decoupled from how the request arrives, it manages memory, function calls, compilation caching and guest execution limits.
4. **Wasm Plugins (`/plugins`)**: The guest code loaded by the operator.

## 2. Technical Decisions

### 2.1 wazero over CGO
**Decision:** Use `github.com/tetratelabs/wazero` instead of traditional C/C++ based libraries (such as Wasmer or Wasmtime).
**Justification:** CGO libraries compromise the portability and security of Go's cross-compilation. `wazero` is 100% native to Go, resulting in a single static binary, which drastically simplifies deployment via Docker or direct downloads, while being extremely performant.

### 2.2 Linear Memory and String Management
**Context:** WebAssembly natively supports only integers and floating points. There is no native concept of a `string`.
**Adopted Solution:** 
1. The Go host validates the guest string ABI: `allocate(i32) -> i32` and a target
   function `(i32, i32) -> i64`. Wazero represents these values as `uint64` in its
   Go calling API; that does not change their Wasm types.
2. The guest allocates memory and returns an offset. The host checks that the
   module has memory, validates results and checks memory bounds before access.
3. Go writes the string as bytes directly into the linear memory slice via the wazero API.
4. The Guest processes it, writes the output to another memory location, and returns a 64-bit bitpack (`(offset << 32) | length`).
5. The Host decodes the pointer and retrieves the final string.

### 2.3 Sandboxing and Security
Running arbitrary code demands heavy restrictions.
- **MaxMemoryPages:** The CLI/gateway configuration limits each guest to 20
  pages of 64 KiB (1.25 MiB). This does not bound the process's total memory:
  compiled modules, host buffers and concurrent requests consume additional resources.
- **Context Timeouts:** Executions use `context.WithTimeout`; the runtime also
  enables `WithCloseOnContextDone(true)`. This option makes cancellation/deadlines
  interrupt guest code, including valid infinite loops, and close that instance.
  The default execution budget is five seconds; this is not a global CPU quota.
- **Restricted WASI:** WASI preview1 is instantiated with the configured
  stdout/stderr writers. Module configuration supplies no host environment,
  filesystem mounts or sockets. The HTTP token stays in the gateway and is not
  added to the guest's payload or environment.
- **HTTP Boundary:** Authentication precedes body reads and invocation. Bodies
  are capped at 1 MiB. Shared-token callers can execute every cached plugin;
  there is no tenant separation or per-plugin authorization.

### 2.4 Pre-warming (Plugin Cache)
Caching `CompiledModule` avoids recompiling the same Wasm bytes on each request.
Instantiation and initialization still consume memory and execution time, and a
module's start function can run guest code. Those steps remain inside the
execution context and its deadline.
`gowasmrunner` scans the configured plugin directory during startup and stores
`CompiledModule` values in a RAM map. Requests reuse compiled modules and create
execution instances. There is no filesystem watcher or automatic hot reload.

## 3. Credentials and transport

Server mode reads `GOWASMRUNNER_API_TOKEN` once. An absent variable permits only a
literal loopback listener with local Host/browser checks. A present empty or
invalid token is a configuration error. A wildcard, remote IP or DNS listener
requires a valid token plus TLS certificate/key files. HTTPS authenticates the
server; the Authorization Bearer token authenticates callers. This is not mTLS.

The gateway compares token digests in constant time and never accepts credentials
from query strings or cookies. Actual TLS state and the connection's peer IP
control transport checks; forwarded headers do not establish security. A TLS
reverse proxy may use HTTP only over a loopback connection to a token-protected
runner. Remote proxy-to-runner connections must also use TLS.

Keys and tokens have no automatic reload or rotation service. Update deployment
secrets and restart to rotate them. There are no accounts, refresh tokens,
per-user sessions or separate plugin permissions. The numeric, string and plugin
CLI modes continue to rely on local operating-system access.
