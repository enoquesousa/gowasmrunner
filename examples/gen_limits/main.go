package main

import (
	"log"
	"os"
)

// Generates two Wasm binaries for security/limit testing:
//
//  1. infinite_loop.wasm - An infinite loop to test execution timeout.
//     WAT: (module (func (export "infinite_loop") (loop (br 0))))
//
//  2. memory_limit.wasm - Requests 100 memory pages (6.4MB) to test memory limits.
//     WAT: (module (memory (export "memory") 100))
func main() {
	// 1. Infinite Loop Wasm (to test Timeout)
	infiniteLoopWasm := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
		0x03, 0x02, 0x01, 0x00,
		0x07, 0x11, 0x01, 0x0d,
		0x69, 0x6e, 0x66, 0x69, 0x6e, 0x69, 0x74, 0x65, 0x5f, 0x6c, 0x6f, 0x6f, 0x70,
		0x00, 0x00,
		0x0a, 0x09, 0x01, 0x07, 0x00, 0x03, 0x40, 0x0c, 0x00, 0x0b, 0x0b,
	}
	if err := os.WriteFile("examples/infinite_loop.wasm", infiniteLoopWasm, 0644); err != nil {
		log.Fatalf("failed to write infinite_loop.wasm: %v", err)
	}

	// 2. Memory Allocation Wasm (to test Limit)
	// Requests 100 pages (6.4MB). Runner limits to 10 pages (640KB).
	memoryLimitWasm := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x05, 0x03, 0x01, 0x00, 0x64, // Memory section: min 100 pages
		0x07, 0x0a, 0x01, 0x06,
		0x6d, 0x65, 0x6d, 0x6f, 0x72, 0x79,
		0x02, 0x00,
	}
	if err := os.WriteFile("examples/memory_limit.wasm", memoryLimitWasm, 0644); err != nil {
		log.Fatalf("failed to write memory_limit.wasm: %v", err)
	}

	log.Println("Created examples/infinite_loop.wasm and examples/memory_limit.wasm")
}
