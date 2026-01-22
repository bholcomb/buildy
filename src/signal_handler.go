package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// ShutdownManager handles graceful shutdown on signals
type ShutdownManager struct {
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	cleanupFns []func()
	mu         sync.Mutex
	shutdownCh chan struct{}
	once       sync.Once
}

// NewShutdownManager creates a new ShutdownManager that listens for interrupt signals
func NewShutdownManager() *ShutdownManager {
	ctx, cancel := context.WithCancel(context.Background())
	
	sm := &ShutdownManager{
		ctx:        ctx,
		cancel:     cancel,
		cleanupFns: []func(){},
		shutdownCh: make(chan struct{}),
	}
	
	// Set up signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)
	
	go func() {
		select {
		case sig := <-sigCh:
			log.Printf("\nReceived signal: %v, initiating graceful shutdown...", sig)
			sm.initiateShutdown()
			
			// Wait for second signal for force quit
			select {
			case <-sigCh:
				log.Printf("Received second signal, forcing exit...")
				os.Exit(1)
			case <-sm.shutdownCh:
				// Normal shutdown completed
			}
		case <-sm.ctx.Done():
			// Context cancelled normally
		}
	}()
	
	return sm
}

// Context returns the shutdown context
func (sm *ShutdownManager) Context() context.Context {
	return sm.ctx
}

// IsShuttingDown returns true if shutdown has been initiated
func (sm *ShutdownManager) IsShuttingDown() bool {
	select {
	case <-sm.ctx.Done():
		return true
	default:
		return false
	}
}

// RegisterCleanup registers a cleanup function to be called on shutdown
func (sm *ShutdownManager) RegisterCleanup(fn func()) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.cleanupFns = append(sm.cleanupFns, fn)
}

// TrackTask increments the wait group for an in-progress task
func (sm *ShutdownManager) TrackTask() {
	sm.wg.Add(1)
}

// TaskDone decrements the wait group when a task completes
func (sm *ShutdownManager) TaskDone() {
	sm.wg.Done()
}

// initiateShutdown begins the graceful shutdown process
func (sm *ShutdownManager) initiateShutdown() {
	sm.once.Do(func() {
		// Cancel context to stop new work
		sm.cancel()
		
		// Wait for in-progress tasks with a timeout
		done := make(chan struct{})
		go func() {
			sm.wg.Wait()
			close(done)
		}()
		
		select {
		case <-done:
			log.Printf("All tasks completed gracefully")
		default:
			log.Printf("Waiting for in-progress tasks to complete...")
			<-done
		}
		
		// Run cleanup functions in reverse order
		sm.mu.Lock()
		cleanups := sm.cleanupFns
		sm.mu.Unlock()
		
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
		
		close(sm.shutdownCh)
	})
}

// Shutdown initiates a clean shutdown (for normal exit)
func (sm *ShutdownManager) Shutdown() {
	sm.initiateShutdown()
}

// CleanupPartialOutputs removes partial output files from interrupted tasks
func CleanupPartialOutputs(outputs []string) {
	for _, output := range outputs {
		if _, err := os.Stat(output); err == nil {
			log.Printf("Cleaning up partial output: %s", output)
			if err := os.Remove(output); err != nil {
				log.Printf("WARNING: Failed to remove partial output %s: %v", output, err)
			}
		}
	}
}

// Global shutdown manager instance
var globalShutdownManager *ShutdownManager

// InitShutdownManager initializes the global shutdown manager
func InitShutdownManager() *ShutdownManager {
	globalShutdownManager = NewShutdownManager()
	return globalShutdownManager
}

// GetShutdownManager returns the global shutdown manager
func GetShutdownManager() *ShutdownManager {
	return globalShutdownManager
}
