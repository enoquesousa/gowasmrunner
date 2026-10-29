// Package engine provides the core WebAssembly execution environment.
// It securely encapsulates the wazero runtime, handles WASI integration,
// enforces resource limits, and manages an in-memory cache of compiled plugins.
//
// The primary entry point is [NewRunner], which creates a configured
// [Runner] instance ready to execute Wasm functions or HTTP requests.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// RunnerConfig defines security boundaries and execution limits for the Wasm environment.
// It must be passed to [NewRunner] during initialization.
type RunnerConfig struct {
	// MaxMemoryPages sets the maximum amount of 64KB pages a module can allocate.
	MaxMemoryPages uint32

	// Timeout enforces a strict deadline on any function execution to prevent infinite loops.
	Timeout time.Duration

	// Stdout defines where guest modules can securely write log output via WASI.
	Stdout io.Writer
}

// Runner handles the secure execution of WebAssembly modules and manages the plugin cache.
// It depends on [RunnerConfig] for security constraints.
//
// Create a new Runner with [NewRunner]:
//
//	runner, err := engine.NewRunner(ctx, cfg)
//	defer runner.Close(ctx)
type Runner struct {
	runtime wazero.Runtime
	config  RunnerConfig
	plugins map[string]wazero.CompiledModule
}

// NewRunner initializes a secure wazero runtime and a new plugin store.
// It automatically integrates [wasi_snapshot_preview1] for secure stdout logging.
func NewRunner(ctx context.Context, cfg RunnerConfig) (*Runner, error) {
	rtCfg := wazero.NewRuntimeConfig().
		WithMemoryLimitPages(cfg.MaxMemoryPages).
		WithCloseOnContextDone(true)

	r := wazero.NewRuntimeWithConfig(ctx, rtCfg)

	_, err := wasi_snapshot_preview1.Instantiate(ctx, r)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("failed to instantiate WASI: %w", err), r.Close(ctx))
	}

	return &Runner{
		runtime: r,
		config:  cfg,
		plugins: make(map[string]wazero.CompiledModule),
	}, nil
}

// LoadPlugins escaneia um diretório e compila todos os arquivos .wasm encontrados.
func (r *Runner) LoadPlugins(ctx context.Context, dirPath string) error {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return fmt.Errorf("failed to read plugins directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".wasm" {
			name := entry.Name()
			path := filepath.Join(dirPath, name)

			wasmBytes, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("failed to read plugin %s: %w", name, err)
			}

			// Compila o módulo e armazena no cache (não instancia ainda)
			compiled, err := r.runtime.CompileModule(ctx, wasmBytes)
			if err != nil {
				return fmt.Errorf("failed to compile plugin %s: %w", name, err)
			}

			r.plugins[name] = compiled
		}
	}
	return nil
}

// RunPlugin executa uma função de um plugin específico usando o módulo em cache.
func (r *Runner) RunPlugin(ctx context.Context, pluginName string, funcName string, params ...uint64) (_ []uint64, err error) {
	compiled, ok := r.plugins[pluginName]
	if !ok {
		return nil, fmt.Errorf("plugin %s not found in cache", pluginName)
	}

	execCtx, cancel := context.WithTimeout(ctx, r.config.Timeout)
	defer cancel()

	modCfg := wazero.NewModuleConfig().
		WithStdout(r.config.Stdout).
		WithStderr(r.config.Stdout)

	// Instancia o módulo a partir do código já compilado (muito mais rápido)
	mod, err := r.runtime.InstantiateModule(execCtx, compiled, modCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to instantiate plugin %s: %w", pluginName, err)
	}
	defer closeModule(execCtx, mod, &err)

	f := mod.ExportedFunction(funcName)
	if f == nil {
		return nil, fmt.Errorf("function %s not found in plugin %s", funcName, pluginName)
	}

	return f.Call(execCtx, params...)
}

// RunPluginString passes a string payload to a cached Wasm plugin and retrieves a string result.
// It leverages linear memory integration: first allocating memory on the guest,
// then writing the host string directly into the guest bounds.
func (r *Runner) RunPluginString(ctx context.Context, pluginName string, funcName string, input string) (_ string, err error) {
	compiled, ok := r.plugins[pluginName]
	if !ok {
		return "", fmt.Errorf("plugin %s not found in cache", pluginName)
	}

	execCtx, cancel := context.WithTimeout(ctx, r.config.Timeout)
	defer cancel()

	modCfg := wazero.NewModuleConfig().
		WithStdout(r.config.Stdout).
		WithStderr(r.config.Stdout)

	mod, err := r.runtime.InstantiateModule(execCtx, compiled, modCfg)
	if err != nil {
		return "", fmt.Errorf("failed to instantiate plugin %s: %w", pluginName, err)
	}
	defer closeModule(execCtx, mod, &err)

	return callString(execCtx, mod, funcName, input)
}

// RunFunction carrega um arquivo Wasm e executa uma função numérica genérica.
func (r *Runner) RunFunction(ctx context.Context, wasmPath string, funcName string, params ...uint64) (_ []uint64, err error) {
	execCtx, cancel := context.WithTimeout(ctx, r.config.Timeout)
	defer cancel()

	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read wasm file: %w", err)
	}

	modCfg := wazero.NewModuleConfig().
		WithStdout(r.config.Stdout).
		WithStderr(r.config.Stdout)

	mod, err := r.runtime.InstantiateWithConfig(execCtx, wasmBytes, modCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to instantiate wasm module: %w", err)
	}
	defer closeModule(execCtx, mod, &err)

	f := mod.ExportedFunction(funcName)
	if f == nil {
		return nil, fmt.Errorf("function %s not found", funcName)
	}

	return f.Call(execCtx, params...)
}

// RunGreet carrega o módulo e executa a lógica de saudação com strings.
func (r *Runner) RunGreet(ctx context.Context, wasmPath string, name string) (_ string, err error) {
	execCtx, cancel := context.WithTimeout(ctx, r.config.Timeout)
	defer cancel()

	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		return "", fmt.Errorf("failed to read wasm file: %w", err)
	}

	modCfg := wazero.NewModuleConfig().
		WithStdout(r.config.Stdout).
		WithStderr(r.config.Stdout)

	mod, err := r.runtime.InstantiateWithConfig(execCtx, wasmBytes, modCfg)
	if err != nil {
		return "", fmt.Errorf("failed to instantiate wasm module: %w", err)
	}
	defer closeModule(execCtx, mod, &err)

	return callString(execCtx, mod, "greet", name)
}

// callString implements the shared memory32 string ABI used by both entry points.
func callString(ctx context.Context, mod api.Module, funcName, input string) (string, error) {
	target, err := stringFunction(mod, funcName, []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}, api.ValueTypeI64)
	if err != nil {
		return "", err
	}
	allocate, err := stringFunction(mod, "allocate", []api.ValueType{api.ValueTypeI32}, api.ValueTypeI32)
	if err != nil {
		return "", err
	}
	memory, err := stringMemory(mod)
	if err != nil {
		return "", err
	}
	inputSize := uint64(len(input))
	if inputSize > uint64(^uint32(0)) {
		return "", fmt.Errorf("input exceeds the 32-bit Wasm string ABI")
	}
	inputPtr, err := callSingleResult(ctx, allocate, inputSize)
	if err != nil {
		return "", fmt.Errorf("failed to allocate memory: %w", err)
	}
	if !memory.Write(uint32(inputPtr), []byte(input)) {
		return "", fmt.Errorf("out of memory bounds when writing string")
	}
	ptrLen, err := callSingleResult(ctx, target, inputPtr, inputSize)
	if err != nil {
		return "", fmt.Errorf("failed to call %s: %w", funcName, err)
	}
	resPtr := uint32(ptrLen >> 32)
	resLen := uint32(ptrLen)
	resBytes, ok := memory.Read(resPtr, resLen)
	if !ok {
		return "", fmt.Errorf("out of memory bounds when reading result")
	}

	return string(resBytes), nil
}

func stringFunction(mod api.Module, name string, params []api.ValueType, result api.ValueType) (api.Function, error) {
	function := mod.ExportedFunction(name)
	if function == nil {
		return nil, fmt.Errorf("function %s not found", name)
	}
	definition := function.Definition()
	if !slices.Equal(definition.ParamTypes(), params) || !slices.Equal(definition.ResultTypes(), []api.ValueType{result}) {
		return nil, fmt.Errorf("invalid string ABI signature for function %s", name)
	}
	return function, nil
}

func stringMemory(mod api.Module) (api.Memory, error) {
	memory := mod.Memory()
	value := reflect.ValueOf(memory)
	// wazero can return a typed nil Memory; preserve support for unexported memory.
	if !value.IsValid() || (value.Kind() == reflect.Pointer && value.IsNil()) {
		return nil, fmt.Errorf("plugin has no linear memory for the string ABI")
	}
	return memory, nil
}

func callSingleResult(ctx context.Context, function api.Function, params ...uint64) (uint64, error) {
	results, err := function.Call(ctx, params...)
	if err != nil {
		return 0, err
	}
	if len(results) != 1 {
		return 0, fmt.Errorf("string ABI function must return exactly one result")
	}
	return results[0], nil
}

func closeModule(ctx context.Context, mod api.Module, executionErr *error) {
	*executionErr = errors.Join(*executionErr, mod.Close(ctx))
}

// Close libera os recursos e o cache.
func (r *Runner) Close(ctx context.Context) error {
	var err error
	for _, p := range r.plugins {
		err = errors.Join(err, p.Close(ctx))
	}
	return errors.Join(err, r.runtime.Close(ctx))
}
