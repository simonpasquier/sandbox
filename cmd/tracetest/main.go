package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"runtime/trace"
	"time"
)

// Example demonstrates the use of the trace package to trace
// the execution of a Go program. The trace output will be
// written to the file trace.out
func main() {
	f, err := os.Create("trace.out")
	if err != nil {
		log.Fatalf("failed to create trace output file: %v", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			log.Fatalf("failed to close trace file: %v", err)
		}
	}()

	if err := trace.Start(f); err != nil {
		log.Fatalf("failed to start trace: %v", err)
	}
	defer trace.Stop()

	run()
}

func run() {
	fmt.Println("this function will be traced")
	ctx := context.Background()
	ctx, trace := trace.NewTask(ctx, "run")
	defer trace.End()
	step1(ctx)
	step2(ctx)
}

func step1(ctx context.Context) {
	defer trace.StartRegion(ctx, "step1").End()
	time.Sleep(100 * time.Millisecond)
	trace.WithRegion(ctx, "step1.1", func() {
		time.Sleep(500 * time.Millisecond)
	})
}

func step2(ctx context.Context) {
	trace.WithRegion(ctx, "step2", func() {
		time.Sleep(200 * time.Millisecond)
	})
}
