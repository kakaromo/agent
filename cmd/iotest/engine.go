package main

import (
	"context"
	"sync"
	"time"
)

// Engine orchestrates multi-threaded I/O test execution.
type Engine struct {
	cfg Config
}

// NewEngine creates a new test engine.
func NewEngine(cfg Config) *Engine {
	return &Engine{cfg: cfg}
}

// Run executes all threads and returns the final result.
func (e *Engine) Run() FinalResult {
	start := time.Now()

	var ctx context.Context
	var cancel context.CancelFunc
	if e.cfg.DurationSeconds > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(e.cfg.DurationSeconds)*time.Second)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}
	defer cancel()

	// Barrier for sync_start
	var barrier sync.WaitGroup
	if e.cfg.SyncStart && len(e.cfg.Threads) > 1 {
		barrier.Add(len(e.cfg.Threads))
	}

	results := make([]ThreadResult, len(e.cfg.Threads))
	var wg sync.WaitGroup

	for idx, threadDef := range e.cfg.Threads {
		wg.Add(1)
		go func(i int, td ThreadDef) {
			defer wg.Done()

			// Wait at barrier if sync_start
			if e.cfg.SyncStart && len(e.cfg.Threads) > 1 {
				barrier.Done()
				barrier.Wait()
			}

			results[i] = e.runThread(ctx, td)
		}(idx, threadDef)
	}

	wg.Wait()
	totalDuration := time.Since(start).Nanoseconds()

	return FinalResult{
		Threads:       results,
		TotalDuration: totalDuration,
	}
}

// runThread executes a single thread's command sequence.
func (e *Engine) runThread(ctx context.Context, td ThreadDef) ThreadResult {
	ts := newThreadState(td.Name)
	start := time.Now()

	for _, cmd := range td.Commands {
		select {
		case <-ctx.Done():
			// Duration timeout reached
			ts.result.Ops = append(ts.result.Ops, OpResult{
				Op: "timeout", Status: "error", Error: "duration timeout reached",
			})
			goto done
		default:
		}
		opResult := ts.executeCommand(cmd)
		ts.result.Ops = append(ts.result.Ops, opResult)
	}

	// Close any remaining open files
	for name, fh := range ts.fds {
		fh.fd.Close()
		delete(ts.fds, name)
	}

done:
	dur := time.Since(start)
	ts.result.Duration = dur.Nanoseconds()
	if dur.Seconds() > 0 {
		ts.result.Throughput = float64(ts.result.TotalBytes) / dur.Seconds()
		ts.result.IOPS = float64(ts.result.TotalOps) / dur.Seconds()
	}

	return *ts.result
}
