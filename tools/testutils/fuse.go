package testutils

import (
	"fmt"
	"os"
	"os/exec"
	"time"
)

// FUSEMount represents a mounted FUSE filesystem
type FUSEMount struct {
	MountPoint string
	ServerURL  string
	PID        int
	Cmd        *exec.Cmd
	LogPath    string
}

// StartFUSE starts a shelley-fuse filesystem
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
	fuseExe := "../bin/shelley-fuse"
	logPath := fmt.Sprintf("%s/fuse.log", mountPoint)
	pidFile := fmt.Sprintf("%s/fuse.pid", mountPoint)

	cmd := exec.Command(fuseExe, mountPoint, serverURL)

	// Capture output
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create log file: %w", err)
	}
	defer logFile.Close()

	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start FUSE: %w", err)
	}

	// Write PID file
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644); err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("failed to write PID file: %w", err)
	}

	// Wait for mount to be ready
	if err := waitForFUSEMount(mountPoint, 10*time.Second); err != nil {
		cmd.Process.Kill()
		os.Remove(pidFile)
		return nil, fmt.Errorf("FUSE mount failed: %w", err)
	}

	return &FUSEMount{
		MountPoint: mountPoint,
		ServerURL:  serverURL,
		PID:        cmd.Process.Pid,
		Cmd:        cmd,
		LogPath:    logPath,
	}, nil
}

// Stop unmounts and stops the FUSE filesystem
func (fm *FUSEMount) Stop() error {
	pidFile := fmt.Sprintf("%s/fuse.pid", fm.MountPoint)
	defer os.Remove(pidFile)

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
	// Change to project root for building
	cmd := exec.Command("go", "build", "-o", "bin/shelley-fuse", "./cmd/shelley-fuse")
	cmd.Dir = ".."
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
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
	// Check if default directory exists and is accessible
	defaultPath := fmt.Sprintf("%s/default", mountPoint)
	if stat, err := os.Stat(defaultPath); err != nil {
		return false
	} else if !stat.IsDir() {
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

// IsUserInFuseGroup checks if current user can use FUSE
func IsUserInFuseGroup() bool {
	// Try a simple mount/unmount test
	tmpDir, err := os.MkdirTemp("", "fuse-test")
	if err != nil {
		return false
	}
	defer os.RemoveAll(tmpDir)

	cmd := exec.Command("fusermount", "-u", tmpDir)
	return cmd.Run() == nil
}