package tests

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/enoquesousa/gowasmrunner/internal/engine"
	"github.com/tetratelabs/wazero/api"
)

func TestEngineRejectsInvalidStringABI(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*wasmModuleFixture)
		want   string
	}{
		{"no memory", func(m *wasmModuleFixture) { m.memory, m.exportMemory = false, false }, "no linear memory"},
		{"missing allocator", func(m *wasmModuleFixture) { m.functions[0].name = "other" }, "allocate not found"},
		{"missing target", func(m *wasmModuleFixture) { m.functions[1].name = "other" }, "greet not found"},
		{"allocator without result", func(m *wasmModuleFixture) { m.functions[0].setResults() }, "signature for function allocate"},
		{"allocator with extra results", func(m *wasmModuleFixture) { m.functions[0].setResults(api.ValueTypeI32, api.ValueTypeI32) }, "signature for function allocate"},
		{"allocator parameter type", func(m *wasmModuleFixture) { m.functions[0].params = []byte{api.ValueTypeI64} }, "signature for function allocate"},
		{"allocator result type", func(m *wasmModuleFixture) { m.functions[0].setResults(api.ValueTypeI64) }, "signature for function allocate"},
		{"target without result", func(m *wasmModuleFixture) { m.functions[1].setResults() }, "signature for function greet"},
		{"target with extra results", func(m *wasmModuleFixture) { m.functions[1].setResults(api.ValueTypeI64, api.ValueTypeI64) }, "signature for function greet"},
		{"target parameter types", func(m *wasmModuleFixture) {
			m.functions[1].params = []byte{api.ValueTypeF64, api.ValueTypeF64}
			m.functions[1].setResults(api.ValueTypeI64)
		}, "signature for function greet"},
		{"target result type", func(m *wasmModuleFixture) { m.functions[1].setResults(api.ValueTypeI32) }, "signature for function greet"},
		{"allocation outside memory", func(m *wasmModuleFixture) { m.functions[0].instructions = []byte{0x41, 0x7f} }, "bounds when writing"},
		{"result outside memory", func(m *wasmModuleFixture) { m.functions[1].instructions = []byte{0x42, 0x7f} }, "bounds when reading"},
	} {
		for _, cached := range []bool{false, true} {
			entry := "RunGreet"
			if cached {
				entry = "RunPluginString"
			}
			t.Run(test.name+"/"+entry, func(t *testing.T) {
				defer func() {
					if value := recover(); value != nil {
						t.Errorf("invalid guest caused a host panic: %v", value)
					}
				}()
				fixture := stringModuleFixture()
				test.change(&fixture)
				result, err := executeStringFixture(t, fixture, cached, "synthetic input")
				if err == nil || !strings.Contains(err.Error(), test.want) || result != "" {
					t.Fatalf("result = %q, error = %v; want %q", result, err, test.want)
				}
			})
		}
	}
}

func TestEngineStringABIPreservesValidMemory(t *testing.T) {
	for _, exported := range []bool{false, true} {
		for _, cached := range []bool{false, true} {
			for _, input := range []string{"", "Olá, 世界"} {
				name := "private memory"
				if exported {
					name = "exported memory"
				}
				if cached {
					name += "/cached"
				} else {
					name += "/file"
				}
				t.Run(name+"/"+input, func(t *testing.T) {
					fixture := stringModuleFixture()
					fixture.exportMemory = exported
					result, err := executeStringFixture(t, fixture, cached, input)
					if err != nil || result != input {
						t.Fatalf("result = %q, error = %v; want %q", result, err, input)
					}
				})
			}
		}
	}
}

func TestEngineCancelsCachedAndStringExecution(t *testing.T) {
	for _, test := range []struct {
		name    string
		fixture wasmModuleFixture
		cached  bool
		strings bool
	}{
		{"cached numeric loop", loopModuleFixture(), true, false},
		{"cached allocator loop", stringLoopFixture("allocate"), true, true},
		{"greet target loop", stringLoopFixture("greet"), false, true},
		{"module start loop", stringLoopFixture("_start"), false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := newSecurityRunner(t, 100*time.Millisecond)
			path := writeSecurityFixture(t, test.fixture)
			if test.cached {
				if err := runner.LoadPlugins(context.Background(), filepath.Dir(path)); err != nil {
					t.Fatalf("compile valid fixture: %v", err)
				}
			}
			var err error
			switch {
			case !test.cached:
				_, err = runner.RunGreet(context.Background(), path, "input")
			case test.strings:
				_, err = runner.RunPluginString(context.Background(), filepath.Base(path), "greet", "input")
			default:
				_, err = runner.RunPlugin(context.Background(), filepath.Base(path), "infinite_loop")
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected an execution deadline error, got %v", err)
			}
			if test.cached && !test.strings {
				results, nextErr := runner.RunPlugin(context.Background(), filepath.Base(path), "ready")
				if nextErr != nil || len(results) != 1 || results[0] != 1 {
					t.Fatalf("cancellation damaged the shared runtime/cache: results=%v, error=%v", results, nextErr)
				}
			}
		})
	}
}

func TestEngineDoesNotInheritHostEnvironment(t *testing.T) {
	t.Setenv("GOWASMRUNNER_TEST_HOST_TOKEN", "synthetic-host-token-only")
	fixture := wasmModuleFixture{
		memory: true, exportMemory: true,
		functions: []wasmFunctionFixture{
			{module: "wasi_snapshot_preview1", name: "environ_sizes_get", params: []byte{api.ValueTypeI32, api.ValueTypeI32}, results: []byte{api.ValueTypeI32}},
			{name: "environment_count", results: []byte{api.ValueTypeI32}, instructions: []byte{
				0x41, 0x00, 0x41, 0x7f, 0x36, 0x02, 0x00, // Store a nonzero sentinel at the result address.
				0x41, 0x00, 0x41, 0x04, 0x10, 0x00, 0x1a, // environ_sizes_get(0, 4), drop errno.
				0x41, 0x00, 0x28, 0x02, 0x00, // Return the environment count written by WASI.
			}},
		},
	}
	path := writeSecurityFixture(t, fixture)
	runner := newSecurityRunner(t, time.Second)
	if err := runner.LoadPlugins(context.Background(), filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	for _, cached := range []bool{false, true} {
		var results []uint64
		var err error
		if cached {
			results, err = runner.RunPlugin(context.Background(), filepath.Base(path), "environment_count")
		} else {
			results, err = runner.RunFunction(context.Background(), path, "environment_count")
		}
		if err != nil || len(results) != 1 || results[0] != 0 {
			t.Fatalf("cached=%t: guest environment count = %v, error = %v; want zero", cached, results, err)
		}
	}
}

func newSecurityRunner(t *testing.T, timeout time.Duration) *engine.Runner {
	t.Helper()
	runner, err := engine.NewRunner(context.Background(), engine.RunnerConfig{
		MaxMemoryPages: 2, Timeout: timeout, Stdout: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := runner.Close(context.Background()); closeErr != nil {
			t.Error(closeErr)
		}
	})
	return runner
}

func executeStringFixture(t *testing.T, fixture wasmModuleFixture, cached bool, input string) (string, error) {
	t.Helper()
	path := writeSecurityFixture(t, fixture)
	runner := newSecurityRunner(t, time.Second)
	if cached {
		if err := runner.LoadPlugins(context.Background(), filepath.Dir(path)); err != nil {
			t.Fatalf("compile fixture: %v", err)
		}
		return runner.RunPluginString(context.Background(), filepath.Base(path), "greet", input)
	}
	return runner.RunGreet(context.Background(), path, input)
}

func writeSecurityFixture(t *testing.T, fixture wasmModuleFixture) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.wasm")
	if err := os.WriteFile(path, fixture.bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

type wasmModuleFixture struct {
	memory, exportMemory bool
	functions            []wasmFunctionFixture // Function imports precede definitions, as in Wasm.
}

type wasmFunctionFixture struct {
	module, name                  string
	params, results, instructions []byte
}

func stringModuleFixture() wasmModuleFixture {
	return wasmModuleFixture{
		memory: true, exportMemory: true,
		functions: []wasmFunctionFixture{
			{name: "allocate", params: []byte{api.ValueTypeI32}, results: []byte{api.ValueTypeI32}, instructions: []byte{0x41, 0x00}},
			{name: "greet", params: []byte{api.ValueTypeI32, api.ValueTypeI32}, results: []byte{api.ValueTypeI64}, instructions: []byte{0x20, 0x01, 0xad}},
		},
	}
}

func loopModuleFixture() wasmModuleFixture {
	return wasmModuleFixture{functions: []wasmFunctionFixture{
		{name: "infinite_loop", instructions: []byte{0x03, 0x40, 0x0c, 0x00, 0x0b}},
		{name: "ready", results: []byte{api.ValueTypeI32}, instructions: []byte{0x41, 0x01}},
	}}
}

func stringLoopFixture(name string) wasmModuleFixture {
	fixture := stringModuleFixture()
	// The unreachable instruction after the loop also validates for non-void results.
	loop := []byte{0x03, 0x40, 0x0c, 0x00, 0x0b, 0x00}
	if name == "_start" {
		fixture.functions = append(fixture.functions, wasmFunctionFixture{name: name, instructions: loop})
	} else {
		for i := range fixture.functions {
			if fixture.functions[i].name == name {
				fixture.functions[i].instructions = loop
			}
		}
	}
	return fixture
}

func (function *wasmFunctionFixture) setResults(results ...byte) {
	function.results = results
	function.instructions = nil
	for _, result := range results {
		switch result {
		case api.ValueTypeI32:
			function.instructions = append(function.instructions, 0x41, 0x00)
		case api.ValueTypeI64:
			function.instructions = append(function.instructions, 0x42, 0x00)
		}
	}
}

// bytes generates small real modules without relying on a Wasm compiler or checked-in binaries.
func (fixture wasmModuleFixture) bytes() []byte {
	module := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	types := wasmU32(uint32(len(fixture.functions)))
	var imports, functions, exports, bodies []byte
	var importCount, functionCount, exportCount uint32
	for index, function := range fixture.functions {
		types = append(types, 0x60)
		types = append(types, wasmU32(uint32(len(function.params)))...)
		types = append(types, function.params...)
		types = append(types, wasmU32(uint32(len(function.results)))...)
		types = append(types, function.results...)
		if function.module != "" {
			imports = append(imports, wasmName(function.module)...)
			imports = append(imports, wasmName(function.name)...)
			imports = append(imports, 0x00)
			imports = append(imports, wasmU32(uint32(index))...)
			importCount++
			continue
		}
		functions = append(functions, wasmU32(uint32(index))...)
		body := append([]byte{0x00}, function.instructions...)
		body = append(body, 0x0b)
		bodies = append(bodies, wasmU32(uint32(len(body)))...)
		bodies = append(bodies, body...)
		functionCount++
		exports = append(exports, wasmName(function.name)...)
		exports = append(exports, 0x00)
		exports = append(exports, wasmU32(uint32(index))...)
		exportCount++
	}
	module = wasmSection(module, 1, types)
	if importCount != 0 {
		module = wasmSection(module, 2, append(wasmU32(importCount), imports...))
	}
	module = wasmSection(module, 3, append(wasmU32(functionCount), functions...))
	if fixture.memory {
		module = wasmSection(module, 5, []byte{0x01, 0x00, 0x01})
		if fixture.exportMemory {
			exports = append(exports, wasmName("memory")...)
			exports = append(exports, 0x02, 0x00)
			exportCount++
		}
	}
	module = wasmSection(module, 7, append(wasmU32(exportCount), exports...))
	return wasmSection(module, 10, append(wasmU32(functionCount), bodies...))
}

func wasmName(value string) []byte {
	return append(wasmU32(uint32(len(value))), []byte(value)...)
}

func wasmSection(module []byte, id byte, payload []byte) []byte {
	module = append(module, id)
	module = append(module, wasmU32(uint32(len(payload)))...)
	return append(module, payload...)
}

func wasmU32(value uint32) []byte {
	var encoded []byte
	for value >= 0x80 {
		encoded = append(encoded, byte(value&0x7f)|0x80)
		value >>= 7
	}
	return append(encoded, byte(value))
}
