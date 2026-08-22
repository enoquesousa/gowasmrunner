<div align="center">
  <h1>Go Wasm Runner</h1>
  <p>An ultra-lightweight serverless execution environment and gateway for WebAssembly modules, written 100% in Go.</p>

  <img src="assets/github-go.png" alt="Go Wasm Runner Banner" width="600px">

  <br>

  ![CI](https://img.shields.io/github/actions/workflow/status/enoquesousa/gowasmrunner/ci.yml?style=flat&logo=githubactions&logoColor=white)
  [![CodeFactor](https://www.codefactor.io/repository/github/enoquesousa/gowasmrunner/badge?style=flat)](https://www.codefactor.io/repository/github/enoquesousa/gowasmrunner)
  [![Go Reference](https://img.shields.io/badge/go.dev-reference-007d9c?style=flat&logo=go&logoColor=white)](https://pkg.go.dev/github.com/enoquesousa/gowasmrunner)
  ![License](https://img.shields.io/github/license/enoquesousa/gowasmrunner?style=flat&color=blue)
  ![Go Version](https://img.shields.io/github/go-mod/go-version/enoquesousa/gowasmrunner?style=flat&logo=go&logoColor=white)
  ![Last Commit](https://img.shields.io/github/last-commit/enoquesousa/gowasmrunner?style=flat)
</div>

---

`gowasmrunner` is an isolated execution environment that allows you to run WebAssembly functions locally via the CLI or expose them instantly as a serverless HTTP API. It solves portability and security issues when running third-party code by enforcing strict memory and execution time boundaries, without relying on CGO dependencies.

## Demonstration

Running a plugin via CLI:
```bash
$ gowasmrunner -mode plugin plugin-add.wasm add 10 20
Plugin plugin-add.wasm [add] result: [30]
```

Running as a Serverless Gateway:
```bash
$ gowasmrunner -mode server -port 8080
2026/04/04 19:30:00 🚀 gowasmrunner HTTP serverless gateway running on port 8080

# In another terminal window:
$ curl -X POST "http://localhost:8080/execute/plugin-greet.wasm?func=greet" -d "World"
Hello, World
```

![gowasmrunner in action](assets/gowasmrunner_in_action.png)

## Technologies & Frameworks

<div align="center">
  <img src="https://img.shields.io/badge/Go-00ADD8?style=flat&logo=go&logoColor=white" alt="Go" />
  <img src="https://img.shields.io/badge/wazero-00ADD8?style=flat&logo=go&logoColor=white" alt="wazero" />
  <img src="https://img.shields.io/badge/WebAssembly-654FF0?style=flat&logo=webassembly&logoColor=white" alt="WebAssembly" />
  <img src="https://img.shields.io/badge/Docker-2496ED?style=flat&logo=docker&logoColor=white" alt="Docker" />
  <img src="https://img.shields.io/badge/WASI-000000?style=flat&logo=webassembly&logoColor=white" alt="WASI" />
</div>

## Prerequisites

- Go >= 1.21
- Docker (optional, for running as a container)

## Installation and Usage

### As a binary

```bash
go install github.com/enoquesousa/gowasmrunner/cmd/runner@latest
```

### From source

```bash
git clone https://github.com/enoquesousa/gowasmrunner.git
cd gowasmrunner
make build
make run
```

### With Docker

```bash
docker build -t gowasmrunner .
docker run -p 8080:8080 gowasmrunner
```

## Makefile Targets

| Target | Description |
|---|---|
| `build` | Compiles the CLI binary into the `bin/` folder |
| `test` | Runs all integration and unit tests |
| `gen-example` | Generates example `.wasm` modules in the `examples/` folder |
| `run` | Builds, generates examples, and executes a simple addition test |
| `clean` | Removes build artifacts and temporary compiled modules |

## Architecture

The project follows a modular architecture focused on isolation between the host runtime and guest modules.

<div align="center">

```mermaid
graph TD
    CLI[CLI / HTTP Client] --> Runner[cmd/runner]

    subgraph "Execution Layer"
        Runner --> Engine[internal/engine]
        Engine --> Store[PluginStore Cache]
        Engine --> Limits[Memory + Timeout Limits]
        Engine --> WASI[WASI Stdout Bridge]
    end

    subgraph "Plugin Layer"
        Store --> WasmGo[Go / TinyGo Plugin]
        Store --> WasmRust[Rust Plugin]
        Store --> WasmCustom[Any .wasm Plugin]
    end

    subgraph "HTTP Gateway"
        Runner --> HTTP[net/http Server :8080]
        HTTP --> Execute["POST /execute/{plugin}"]
        Execute --> Engine
    end

    style Engine fill:#2da44e,stroke:#fff,stroke-width:1px,color:#fff
    style HTTP fill:#3498db,stroke:#fff,stroke-width:1px,color:#fff
    style Store fill:#8e44ad,stroke:#fff,stroke-width:1px,color:#fff
```

</div>

- `cmd/runner`: Entry point that manages the CLI and the HTTP Server.
- `internal/engine`: The core system. Manages the `wazero` lifecycle, resource limits (memory/timeout), WASI integration, and the compiled modules cache (`PluginStore`).
- `plugins/`: Default directory scanned by the system for module pre-warming.

See [docs/architecture.md](docs/architecture.md) for more details on technical decisions.

## API Reference

### Execute Plugin

`POST /execute/{plugin_name}`

Executes an exported function from a cached Wasm module.

**Query Parameters:**
- `func` (optional): Name of the function to execute. Default: `greet`.

**Body:**
Raw payload (text/plain, application/json, etc) that will be passed to the Wasm module's linear memory.

**Response:**
The result returned by the Wasm function, text-encoded in the response body.

## Configuration

Current configurations are managed via command line flags:

| Flag | Description | Type | Default |
|---|---|---|---|
| `-mode` | Operation mode (`numeric`, `string`, `plugin`, `server`) | string | `numeric` |
| `-wasm` | Direct path to a Wasm file (num/str modes) | string | `""` |
| `-plugins` | Directory to load cached plugins from | string | `./plugins` |
| `-func` | Default function to be executed | string | `add` |
| `-port` | Port for the HTTP server | string | `8080` |

## Roadmap (Implemented Phases)

- [x] **Phase 1: The Host (Basic Wasm Runtime)**
  - **Objective:** Configure the runtime and execute a simple arithmetic function compiled in Wasm.
  - **What was done:** Utilized the Wazero library (100% Go, no CGO dependency) to load a `.wasm` file and call an exported function via command line arguments.

- [x] **Phase 2: The Bridge (Memory and Data Exchange)**
  - **Objective:** Overcome Wasm's limitation of only handling numbers by enabling the passing of strings and complex objects.
  - **What was done:** Implemented memory allocation logic in the guest (Wasm) and buffer read/write operations in the host (Go) to pass and return greeting strings.

- [x] **Phase 3: The Warden (Sandboxing and Resources)**
  - **Objective:** Ensure the Wasm module does not consume all server resources, which is essential for the serverless model.
  - **What was done:** Configured memory limits (`MaxMemoryPages`) and execution timeouts (`context.WithTimeout`) for the Wasm instance, alongside basic WASI support for secure host console logging.

- [x] **Phase 4: The Registry (Dynamic Plugin System)**
  - **Objective:** Transform the executor into a platform that loads and manages multiple modules "on-the-fly".
  - **What was done:** Created a `PluginStore` that monitors the `/plugins` folder, pre-compiles the modules (`CompiledModule`), and caches them in memory for ultra-fast invocations via CLI or server.

- [x] **Phase 5: The Gateway (Serverless HTTP Interface)**
  - **Objective:** Expose Wasm modules through an HTTP API, simulating the behavior of AWS Lambda or Cloudflare Workers.
  - **What was done:** Developed an HTTP server (port 8080) where the `/execute/{plugin_name}` path routes the request (POST body) to the respective Wasm plugin. Included a lean `Dockerfile` (Alpine) ready for deployment.

- [x] **The Cherry on Top (Language Agnosticism)**
  - The environment proves to be language-agnostic, allowing functions written in languages like **Rust** or **TinyGo** to be easily compiled to `.wasm` and placed in the examples/plugins folder for identical execution.

## Contributing

Check our [CONTRIBUTING.md](CONTRIBUTING.md) to learn how to set up your environment, run tests, and submit Pull Requests.

## License

Distributed under the MIT License. See [LICENSE](LICENSE) for more information.

<div align="center">

## Author

**Enoque Sousa**

[![LinkedIn](https://img.shields.io/badge/LinkedIn-0077B5?style=flat&logo=linkedin&logoColor=white)](https://www.linkedin.com/in/enoque-sousa-bb89aa168/)
[![GitHub](https://img.shields.io/badge/GitHub-100000?style=flat&logo=github&logoColor=white)](https://github.com/enoquesousa)
[![Portfolio](https://img.shields.io/badge/Portfolio-FF5722?style=flat&logo=target&logoColor=white)](https://enoquesousa.vercel.app)

**[⬆ Back to Top](#go-wasm-runner)**

Made with ❤️ by [Enoque Sousa](https://github.com/enoquesousa)

**Project Status:** Archived — Study Project

</div>
