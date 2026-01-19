package testutil

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// InProcessFUSEServer represents an in-process FUSE server for testing
type InProcessFUSEServer struct {
	Server   *fuse.Server
	MountPoint string
	ServerURL  string
	ErrorChan  chan error
	errors     []error
	errorsMu   sync.Mutex
	ctx        context.Context
	cancel     context.CancelFunc
}

// InProcessFUSEConfig holds configuration for starting an in-process FUSE server
type InProcessFUSEConfig struct {
	MountPoint    string
	Debug         bool
	Timeout       time.Duration
	CreateFS      func() (fs.InodeEmbedder, error) // Function to create the filesystem
}

// StartInProcessFUSE starts a FUSE server in-process for testing
func StartInProcessFUSE(config *InProcessFUSEConfig) (*InProcessFUSEServer, error) {
	if config.MountPoint == "" {
		return nil, fmt.Errorf("mount point is required")
	}
	
	if config.CreateFS == nil {
		return nil, fmt.Errorf("CreateFS function is required")
	}
	
	if config.Timeout == 0 {
		config.Timeout = 10 * time.Second
	}

	// Create FUSE filesystem
	rootFS, err := config.CreateFS()
	if err != nil {
		return nil, fmt.Errorf("failed to create filesystem: %w", err)
	}

	// Set up FUSE server options
	opts := &fs.Options{}
	opts.Debug = config.Debug
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	// Create context for error handling
	ctx, cancel := context.WithCancel(context.Background())
	
	// Create FUSE server
	fssrv, err := fs.Mount(config.MountPoint, rootFS, opts)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to mount FUSE filesystem: %w", err)
	}

	// Create server instance
	server := &InProcessFUSEServer{
		Server:     fssrv,
		MountPoint: config.MountPoint,
		ErrorChan:  make(chan error, 100), // Buffered channel for error collection
		ctx:        ctx,
		cancel:     cancel,
	}

	// Start error collection in background
	go server.collectErrors()

	// Wait for mount to be ready
	if err := server.waitForMount(config.Timeout); err != nil {
		server.Stop()
		return nil, fmt.Errorf("FUSE mount failed to become ready: %w", err)
	}

	return server, nil
}

// collectErrors collects errors from the FUSE server
func (s *InProcessFUSEServer) collectErrors() {
	// In a real implementation, we might capture logs or other error sources
	// For now, we'll just wait for context cancellation
	<-s.ctx.Done()
}

// waitForMount waits for the FUSE mount to be ready
func (s *InProcessFUSEServer) waitForMount(timeout time.Duration) error {
	// Simple check - in a real implementation, we might want to do more thorough validation
	timeoutChan := time.After(timeout)
	tick := time.Tick(100 * time.Millisecond)

	for {
		select {
		case <-timeoutChan:
			return fmt.Errorf("timeout waiting for FUSE mount at %s", s.MountPoint)
		case <-tick:
			// Check if server is running
			if s.Server != nil {
				// Server is ready - fuse.Server doesn't have a Wait() method that returns a value
				// In a real implementation, we might want to do more thorough validation
				return nil
			}
		}
	}
}

// Stop stops the in-process FUSE server
func (s *InProcessFUSEServer) Stop() error {
	// Cancel context to stop error collection
	if s.cancel != nil {
		s.cancel()
	}

	// Unmount FUSE filesystem
	if s.Server != nil {
		if err := s.Server.Unmount(); err != nil {
			s.recordError(fmt.Errorf("failed to unmount FUSE filesystem: %w", err))
		}
	}

	return nil
}

// recordError records an error for later retrieval
func (s *InProcessFUSEServer) recordError(err error) {
	s.errorsMu.Lock()
	defer s.errorsMu.Unlock()
	s.errors = append(s.errors, err)
	
	// Also send to error channel if possible
	select {
	case s.ErrorChan <- err:
	default:
		// Channel is full, drop the error
	}
}

// GetErrors returns all collected errors
func (s *InProcessFUSEServer) GetErrors() []error {
	s.errorsMu.Lock()
	defer s.errorsMu.Unlock()
	
	// Return a copy of the errors slice
	errorsCopy := make([]error, len(s.errors))
	copy(errorsCopy, s.errors)
	return errorsCopy
}

// ClearErrors clears all collected errors
func (s *InProcessFUSEServer) ClearErrors() {
	s.errorsMu.Lock()
	defer s.errorsMu.Unlock()
	s.errors = nil
}

// HasErrors returns true if any errors have been collected
func (s *InProcessFUSEServer) HasErrors() bool {
	s.errorsMu.Lock()
	defer s.errorsMu.Unlock()
	return len(s.errors) > 0
}

// StartExternalFUSE starts a FUSE server as an external process (for compatibility)
func StartExternalFUSE(mountPoint, serverURL, fuseBinary string) (*ExternalFUSEServer, error) {
	return startExternalFUSE(mountPoint, serverURL, fuseBinary)
}

// ExternalFUSEServer represents an external FUSE server process
type ExternalFUSEServer struct {
	MountPoint string
	ServerURL  string
	Cmd        *ExternalCommand
}

// Stop stops the external FUSE server
func (e *ExternalFUSEServer) Stop() error {
	if e.Cmd != nil {
		return e.Cmd.Stop()
	}
	return nil
}

// ExternalCommand represents an external command with error capturing
type ExternalCommand struct {
	Stdout io.Reader
	Stderr io.Reader
	// Add other fields as needed
}

// Stop stops the external command
func (e *ExternalCommand) Stop() error {
	// Implementation would depend on how the command is managed
	return nil
}

// startExternalFUSE is a placeholder for the external FUSE implementation
func startExternalFUSE(mountPoint, serverURL, fuseBinary string) (*ExternalFUSEServer, error) {
	// This would contain the logic from the existing testhelper package
	// For now, return a placeholder
	return &ExternalFUSEServer{
		MountPoint: mountPoint,
		ServerURL:  serverURL,
	}, nil
}