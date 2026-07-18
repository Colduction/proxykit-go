package proxypool_test

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"

	"github.com/colduction/proxykit-go/proxypool"
)

func TestConcurrentLifecycle(t *testing.T) {
	path, _ := makeProxyFile(t, 500)
	tests := []struct {
		name string
		mode proxypool.Mode
	}{
		{name: "sequential", mode: proxypool.ModeSequential},
		{name: "shuffled", mode: proxypool.ModeShuffled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool, err := proxypool.Open(path, proxypool.Options{
				Mode:         test.mode,
				Reuse:        true,
				BlockBytes:   256,
				RegionBytes:  1 << 10,
				MaxLineBytes: 128,
				Seed:         1,
			})
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer pool.Close()

			const (
				readers       = 4
				warmupCalls   = 32
				concurrentOps = 256
			)
			failures := make(chan error, readers+3)
			concurrentStart := make(chan struct{})
			var ready, wait sync.WaitGroup
			ready.Add(readers + 2)

			for reader := range readers {
				wait.Go(func() {
					var buffer []byte
					read := func(iteration int) error {
						if (reader+iteration)&1 == 0 {
							line, nextErr := pool.Next()
							if nextErr == nil && line == "" {
								return errors.New("Next returned an empty line")
							}
							return nextErr
						}
						var nextErr error
						buffer, nextErr = pool.NextBytes(buffer[:0])
						if nextErr == nil && len(buffer) == 0 {
							return errors.New("NextBytes returned an empty line")
						}
						return nextErr
					}
					var warmupErr error
					for iteration := range warmupCalls {
						if warmupErr = read(iteration); warmupErr != nil {
							break
						}
					}
					ready.Done()
					if warmupErr != nil {
						failures <- fmt.Errorf("reader %d warmup: %w", reader, warmupErr)
						return
					}
					<-concurrentStart
					for iteration := range concurrentOps {
						if nextErr := read(iteration); nextErr != nil {
							if errors.Is(nextErr, proxypool.ErrClosed) {
								return
							}
							failures <- fmt.Errorf("reader %d: %w", reader, nextErr)
							return
						}
					}
				})
			}

			wait.Go(func() {
				check := func() error {
					stats := pool.Stats()
					if stats.Mode != test.mode || stats.FileSize <= 0 || stats.MaxLineBytes != 128 {
						return fmt.Errorf("incoherent stats: %+v", stats)
					}
					return nil
				}
				var warmupErr error
				for range warmupCalls {
					if warmupErr = check(); warmupErr != nil {
						break
					}
				}
				ready.Done()
				if warmupErr != nil {
					failures <- warmupErr
					return
				}
				<-concurrentStart
				for range concurrentOps {
					if statsErr := check(); statsErr != nil {
						failures <- statsErr
						return
					}
				}
			})

			wait.Go(func() {
				var warmupErr error
				for range warmupCalls {
					if warmupErr = pool.Reset(); warmupErr != nil {
						break
					}
				}
				ready.Done()
				if warmupErr != nil {
					failures <- fmt.Errorf("Reset warmup: %w", warmupErr)
					return
				}
				<-concurrentStart
				for range concurrentOps {
					if resetErr := pool.Reset(); resetErr != nil {
						if errors.Is(resetErr, proxypool.ErrClosed) {
							return
						}
						failures <- fmt.Errorf("Reset: %w", resetErr)
						return
					}
				}
			})

			wait.Go(func() {
				<-concurrentStart
				for range 16 {
					pool.Stats()
					runtime.Gosched()
				}
				if closeErr := pool.Close(); closeErr != nil {
					failures <- fmt.Errorf("Close: %w", closeErr)
					return
				}
				if closeErr := pool.Close(); closeErr != nil {
					failures <- fmt.Errorf("second Close: %w", closeErr)
				}
			})

			ready.Wait()
			close(concurrentStart)
			wait.Wait()
			close(failures)
			for failure := range failures {
				t.Error(failure)
			}
			if stats := pool.Stats(); !stats.Closed {
				t.Errorf("Stats after Close: %+v", stats)
			}
		})
	}
}
