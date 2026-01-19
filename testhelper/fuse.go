package testhelper

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"shelley-fuse/testutil"
)

// FUSEMount represents a mounted FUSE filesystem
type FUSEMount struct {
	MountPoint string
	Server     *testutil.InProcessFUSEServer
	Cmd        *exec.Cmd
}

// StartFUSE starts a shelley-fuse filesystem (external process)
func StartFUSE(mountPoint, serverURL string) (*FUSEMount, error) {
	if mountPoint == "" {
		mountPoint = "/tmp/shelley-fuse-test"
	}

	// Create mount point
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		return nil, fmt.Errorf("failed to create mount point: %w", err)
	}

	// Check mount point exists and is directory
	if stat, err := os.Stat(mountPoint); err != nil {
		return nil, fmt.Errorf("mount point does not exist: %w", err)
	} else if !stat.IsDir() {
		return nil, fmt.Errorf("mount point is not a directory")
	}

	// Build FUSE binary
	if err := buildFUSE(); err != nil {
		return nil, fmt.Errorf("failed to build FUSE binary: %w", err)
	}

	// Start FUSE process
	fuseExe := "/home/exedev/shelley-fuse/bin/shelley-fuse"
	
	cmd := exec.Command(fuseExe, mountPoint, serverURL)
	// Capture stdout and stderr for debugging
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Start the process
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start FUSE: %w", err)
	}

	// Wait for mount to be ready
	if err := waitForFUSEMount(mountPoint, 10*time.Second); err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("FUSE mount failed: %w", err)
	}

	return &FUSEMount{
		MountPoint: mountPoint,
		Cmd:        cmd,
	}, nil
}

// StartFUSEInProcess starts a FUSE filesystem in-process for better error reporting
func StartFUSEInProcess(mountPoint, serverURL string, createFS func(string) (fs.InodeEmbedder, error)) (*FUSEMount, error) {
	// Create a function that creates the FUSE filesystem
	wrappedCreateFS := func() (fs.InodeEmbedder, error) {
		return createFS(serverURL)
	}
	
	config := &testutil.InProcessFUSEConfig{
		MountPoint: mountPoint,
		CreateFS:   wrappedCreateFS,
	}
	
	server, err := testutil.StartInProcessFUSE(config)
	if err != nil {
		return nil, fmt.Errorf("failed to start in-process FUSE: %w", err)
	}

	return &FUSEMount{
		MountPoint: mountPoint,
		Server:     server,
	}, nil
}

// Stop unmounts and stops the FUSE filesystem
func (fm *FUSEMount) Stop() error {
	// Handle in-process server
	if fm.Server != nil {
		return fm.Server.Stop()
	}

	// Handle external process
	// Unmount first
	if err := unmountFUSE(fm.MountPoint); err != nil {
		fmt.Printf("Warning: failed to unmount %s: %v\n", fm.MountPoint, err)
	}

	// Kill process
	if fm.Cmd != nil && fm.Cmd.Process != nil {
		if err := fm.Cmd.Process.Kill(); err != nil {
			return fmt.Errorf("failed to kill FUSE process: %w", err)
		}
		fm.Cmd.Wait()
	}

	return nil
}

// buildFUSE builds the shelley-fuse binary
func buildFUSE() error {
	// Build from project root
	cmd := exec.Command("go", "build", "-o", "bin/shelley-fuse", "./cmd/shelley-fuse")
	cmd.Dir = "/home/exedev/shelley-fuse"  // Use absolute path to project root
	return cmd.Run()
}

// waitForFUSEMount waits for FUSE mount to be ready
func waitForFUSEMount(mountPoint string, timeout time.Duration) error {
	timeoutChan := time.After(timeout)
	tick := time.Tick(100 * time.Millisecond)

	for {
		select {
		case <-timeoutChan:
			return fmt.Errorf("timeout waiting for FUSE mount at %s", mountPoint)
		case <-tick:
			// Check if mount point is mounted and accessible
			if isFUSEMounted(mountPoint) {
				return nil
			}
		}
	}
}

// isFUSEMounted checks if FUSE filesystem is properly mounted
func isFUSEMounted(mountPoint string) bool {
	// Check if models file exists and is accessible
	modelsPath := fmt.Sprintf("%s/models", mountPoint)
	if stat, err := os.Stat(modelsPath); err != nil {
		return false
	} else if stat.IsDir() {
		return false
	}
	return true
}

// unmountFUSE unmounts a FUSE filesystem
func unmountFUSE(mountPoint string) error {
	cmd := exec.Command("fusermount", "-u", mountPoint)
	return cmd.Run()
}

// UnmountFUSE is the exported version for use in other packages
func UnmountFUSE(mountPoint string) error {
	return unmountFUSE(mountPoint)
}