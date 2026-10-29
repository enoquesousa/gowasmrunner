// Package main implements the CLI application and Serverless Gateway for gowasmrunner.
// It manages parsing arguments, bootstrapping the wazero environment, and orchestrating
// HTTP requests to underlying WebAssembly modules.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/enoquesousa/gowasmrunner/internal/engine"
	"github.com/enoquesousa/gowasmrunner/internal/gateway"
)

func main() {
	// Define command line flags.
	wasmPath := flag.String("wasm", "", "Path to the WebAssembly module file")
	mode := flag.String("mode", "numeric", "Execution mode: 'numeric', 'string', 'plugin' or 'server'")
	pluginDir := flag.String("plugins", "./plugins", "Directory to scan for plugins")
	funcName := flag.String("func", "add", "The name of the exported function to invoke")
	port := flag.String("port", "8080", "Port for the HTTP server to listen on")
	host := flag.String("host", "127.0.0.1", "HTTP bind host; remote addresses require a token and TLS")
	tlsCert := flag.String("tls-cert", "", "Path to the PEM HTTPS certificate chain")
	tlsKey := flag.String("tls-key", "", "Path to the PEM HTTPS private key")
	flag.Parse()

	if *wasmPath == "" && *mode != "plugin" && *mode != "server" {
		printUsageAndExit()
	}

	ctx := context.Background()

	// Configuration of the Runner with security limits.
	cfg := engine.RunnerConfig{
		MaxMemoryPages: 20,              // Increased to support larger plugins
		Timeout:        5 * time.Second, // 5 seconds timeout
		Stdout:         os.Stdout,
	}

	// Initialize the Runner.
	runner, err := engine.NewRunner(ctx, cfg)
	if err != nil {
		log.Fatalf("Error creating runner: %v", err)
	}
	defer func() {
		if closeErr := runner.Close(ctx); closeErr != nil {
			log.Print("Error closing WebAssembly runtime")
		}
	}()

	switch *mode {
	case "server":
		serverConfig := gateway.Config{Host: *host, Port: *port, TLSCert: *tlsCert, TLSKey: *tlsKey}
		if token, configured := os.LookupEnv("GOWASMRUNNER_API_TOKEN"); configured {
			serverConfig.APIToken = &token
		}
		runServerMode(ctx, runner, serverConfig, *pluginDir)
	case "plugin":
		runPluginMode(ctx, runner, flag.Args(), *pluginDir)
	case "string":
		runStringMode(ctx, runner, *wasmPath, flag.Args())
	case "numeric":
		runNumericMode(ctx, runner, *wasmPath, *funcName, flag.Args())
	default:
		log.Fatalf("Unknown execution mode: %s", *mode)
	}
}

func printUsageAndExit() {
	fmt.Println("Usage:")
	fmt.Println("  Numeric/String: gowasmrunner -wasm <path> [-mode string|numeric] [args...]")
	fmt.Println("  Plugin:         gowasmrunner -mode plugin <plugin_name> <func_name> [args...]")
	fmt.Println("  Server:         gowasmrunner -mode server [-host 127.0.0.1] [-port 8080] [-plugins ./plugins]")
	flag.PrintDefaults()
	os.Exit(1)
}

func runServerMode(ctx context.Context, runner *engine.Runner, config gateway.Config, pluginDir string) {
	server, err := gateway.NewServer(config, runner)
	if err != nil {
		log.Fatalf("Invalid HTTP gateway configuration: %v", err)
	}
	if loadErr := runner.LoadPlugins(ctx, pluginDir); loadErr != nil {
		log.Fatalf("Error loading plugins: %v", loadErr)
	}

	log.Printf("HTTP gateway listening at %s", strconv.Quote(server.Addr))
	log.Printf("Plugins loaded from: %s", strconv.Quote(pluginDir))
	if server.TLSConfig != nil {
		err = server.ListenAndServeTLS("", "")
	} else {
		err = server.ListenAndServe()
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("Server failed: %v", err)
	}
}

func runPluginMode(ctx context.Context, runner *engine.Runner, args []string, pluginDir string) {
	if err := runner.LoadPlugins(ctx, pluginDir); err != nil {
		log.Fatalf("Error loading plugins: %v", err)
	}

	if len(args) < 2 {
		log.Fatal("Usage for plugin mode: gowasmrunner -mode plugin <plugin_file.wasm> <func_name> [args...]")
	}

	pluginFile := args[0]
	targetFunc := args[1]

	uintParams := parseUintArgs(args[2:])

	results, err := runner.RunPlugin(ctx, pluginFile, targetFunc, uintParams...)
	if err != nil {
		log.Fatalf("Error executing plugin: %v", err)
	}
	fmt.Printf("Plugin %s [%s] result: %v\n", pluginFile, targetFunc, results)
}

func runStringMode(ctx context.Context, runner *engine.Runner, wasmPath string, args []string) {
	if len(args) < 1 {
		log.Fatal("Missing name argument for string mode")
	}

	result, err := runner.RunGreet(ctx, wasmPath, args[0])
	if err != nil {
		log.Fatalf("Error executing wasm greeting: %v", err)
	}
	fmt.Printf("Wasm Result: %s\n", result)
}

func runNumericMode(ctx context.Context, runner *engine.Runner, wasmPath string, funcName string, args []string) {
	uintParams := parseUintArgs(args)

	results, err := runner.RunFunction(ctx, wasmPath, funcName, uintParams...)
	if err != nil {
		log.Fatalf("Error executing wasm function: %v", err)
	}
	fmt.Printf("Result(s) of %s(%v): %v\n", funcName, args, results)
}

func parseUintArgs(args []string) []uint64 {
	var uintParams []uint64
	for _, arg := range args {
		val, err := strconv.ParseUint(arg, 10, 64)
		if err != nil {
			log.Fatalf("Invalid argument %q: must be a positive integer", arg)
		}
		uintParams = append(uintParams, val)
	}
	return uintParams
}
