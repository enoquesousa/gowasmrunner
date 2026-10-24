package tests

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/enoquesousa/gowasmrunner/internal/engine"
)

func TestWasmRunner(t *testing.T) {
	ctx := context.Background()

	cfg := engine.RunnerConfig{
		MaxMemoryPages: 10,
		Timeout:        2 * time.Second,
		Stdout:         os.Stdout,
	}

	runner, err := engine.NewRunner(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to create runner: %v", err)
	}
	defer func() {
		if closeErr := runner.Close(ctx); closeErr != nil {
			t.Error(closeErr)
		}
	}()

	// Subtest 1: Numeric Operation (Addition)
	t.Run("Numeric Addition", func(t *testing.T) {
		wasmPath := filepath.Join("..", "examples", "add.wasm")

		// Check if the file exists, otherwise skip (must be generated first)
		if _, err := os.Stat(wasmPath); os.IsNotExist(err) {
			t.Skip("add.wasm not found, run generators first")
		}

		results, err := runner.RunFunction(ctx, wasmPath, "add", 10, 20)
		if err != nil {
			t.Fatalf("RunFunction failed: %v", err)
		}

		if len(results) == 0 || results[0] != 30 {
			t.Errorf("expected 30, got %v", results)
		}
	})

	// Subtest 2: String Manipulation (Greeting)
	t.Run("String Greeting", func(t *testing.T) {
		wasmPath := filepath.Join("..", "examples", "greet.wasm")

		if _, err := os.Stat(wasmPath); os.IsNotExist(err) {
			t.Skip("greet.wasm not found, run generators first")
		}

		name := "Gemini"
		expected := "Hello, Gemini"

		result, err := runner.RunGreet(ctx, wasmPath, name)
		if err != nil {
			t.Fatalf("RunGreet failed: %v", err)
		}

		if result != expected {
			t.Errorf("expected %q, got %q", expected, result)
		}
	})

	// Subtest 3: Cross-Language Execution (Rust-style Fibonacci)
	t.Run("Rust Fibonacci", func(t *testing.T) {
		wasmPath := filepath.Join("..", "examples", "rust_fibonacci.wasm")

		if _, err := os.Stat(wasmPath); os.IsNotExist(err) {
			t.Skip("rust_fibonacci.wasm not found, run generators first")
		}

		// Table-driven tests covering base cases and known Fibonacci values
		cases := []struct {
			input    uint64
			expected uint64
		}{
			{0, 0},
			{1, 1},
			{2, 1},
			{5, 5},
			{10, 55},
			{20, 6765},
		}

		for _, tc := range cases {
			results, err := runner.RunFunction(ctx, wasmPath, "fibonacci", tc.input)
			if err != nil {
				t.Fatalf("fibonacci(%d) failed: %v", tc.input, err)
			}
			if len(results) == 0 || results[0] != tc.expected {
				t.Errorf("fibonacci(%d) = %v, expected %d", tc.input, results, tc.expected)
			}
		}
	})

	// Subtest 4: Execution Timeout
	t.Run("Execution Timeout", func(t *testing.T) {
		wasmPath := writeSecurityFixture(t, loopModuleFixture())

		// Create a runner with a short timeout for the test
		shortTimeoutCfg := cfg
		shortTimeoutCfg.Timeout = 100 * time.Millisecond
		r, err := engine.NewRunner(ctx, shortTimeoutCfg)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if closeErr := r.Close(ctx); closeErr != nil {
				t.Error(closeErr)
			}
		}()

		// Verify the fixture compiles and runs before exercising its endless loop.
		ready, err := r.RunFunction(ctx, wasmPath, "ready")
		if err != nil || len(ready) != 1 || ready[0] != 1 {
			t.Fatalf("invalid timeout fixture: results=%v, error=%v", ready, err)
		}
		_, err = r.RunFunction(ctx, wasmPath, "infinite_loop")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("expected execution deadline error, got %v", err)
		}
	})

	// Subtest 5: Memory Limit
	t.Run("Memory Limit", func(t *testing.T) {
		wasmPath := filepath.Join("..", "examples", "memory_limit.wasm")

		// Try to instantiate a module that requests 100 pages (6.4MB)
		// Our runner limits to 10 pages (640KB).
		_, err := runner.RunFunction(ctx, wasmPath, "dummy")
		if err == nil {
			t.Error("expected memory limit error, got nil")
		}
	})
}
