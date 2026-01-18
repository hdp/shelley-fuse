package main

import (
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"shelley-fuse/tools/testutils"
)

func main() {
	var (
		mountPoint = flag.String("mount", "/tmp/shelley-fuse-test", "Mount point directory")
		serverURL  = flag.String("server", "", "Shelley server URL (auto-generated if not specified)")
		serverPort = flag.Int("server-port", 0, "Port of running test server (auto-find if not specified)")
		startServer = flag.Bool("start-server", true, "Start a test server if none is running")
		stop       = flag.Bool("stop", false, "Stop FUSE mount")
	)
	flag.Parse()

	if *stop {
		pidFile := fmt.Sprintf("%s/fuse.pid", *mountPoint)
		if err := stopFUSE(pidFile); err != nil {
			log.Fatalf("Failed to stop FUSE: %v", err)
		}
		return
	}

	var serverURLFinal string
	var err error

	// Determine server URL
	if *serverURL != "" {
		// Validate provided URL
		if _, err := url.Parse(*serverURL); err != nil {
			log.Fatalf("Invalid server URL: %v", err)
		}
		serverURLFinal = *serverURL
	} else {
		// Find or start a test server
		serverURLFinal, err = findOrStartTestServer(*serverPort, *startServer)
		if err != nil {
			log.Fatalf("Failed to get server URL: %v", err)
		}
	}

	// Start FUSE mount
	mount, err := testutils.StartFUSE(*mountPoint, serverURLFinal)
	if err != nil {
		log.Fatalf("Failed to start FUSE mount: %v", err)
	}

	fmt.Printf("✓ Shelley FUSE filesystem mounted successfully!\n")
	fmt.Printf("Mount point: %s\n", mount.MountPoint)
	fmt.Printf("Server URL: %s\n", mount.ServerURL)
	fmt.Printf("Log file: %s\n", mount.LogPath)
	fmt.Printf("\n")
	fmt.Printf("Try these commands:\n")
	fmt.Printf("  ls %s/default/\n", mount.MountPoint)
	fmt.Printf("  cat %s/default/models\n", mount.MountPoint)
	fmt.Printf("  echo 'Hello, Shelley!' > %s/default/model/predictable/new/test\n", mount.MountPoint)
	fmt.Printf("\n")
	fmt.Printf("To stop the mount, run:\n")
	fmt.Printf("  %s -stop -mount %s\n", os.Args[0], *mountPoint)
	fmt.Printf("\n")

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Wait for interrupt signal
	<-sigChan
	fmt.Printf("\nShutting down FUSE mount...\n")

	if err := mount.Stop(); err != nil {
		log.Printf("Error stopping FUSE mount: %v", err)
	} else {
		fmt.Printf("FUSE mount stopped\n")
	}
}

func findOrStartTestServer(port int, startServer bool) (string, error) {
	// If port is specified, check if server is running
	if port > 0 {
		pidFile := fmt.Sprintf("/tmp/shelley-server-%d.pid", port)
		if pidData, err := os.ReadFile(pidFile); err == nil {
			var pid int
			if _, err := fmt.Sscanf(string(pidData), "%d", &pid); err == nil {
				if process, err := os.FindProcess(pid); err == nil {
					if err := process.Signal(syscall.Signal(0)); err == nil {
						// Server is running
						serverURL := fmt.Sprintf("http://localhost:%d", port)
						fmt.Printf("Found running test server at %s\n", serverURL)
						return serverURL, nil
					}
				}
			}
		}
		// Server not running on specified port
		return "", fmt.Errorf("server not running on port %d", port)
	}

	// Find an available port and check if server is running
	for checkPort := 11002; checkPort <= 11100; checkPort++ {
		pidFile := fmt.Sprintf("/tmp/shelley-server-%d.pid", checkPort)
		if pidData, err := os.ReadFile(pidFile); err == nil {
			var pid int
			if _, err := fmt.Sscanf(string(pidData), "%d", &pid); err == nil {
				if process, err := os.FindProcess(pid); err == nil {
					if err := process.Signal(syscall.Signal(0)); err == nil {
						// Found running server
						serverURL := fmt.Sprintf("http://localhost:%d", checkPort)
						fmt.Printf("Found running test server at %s\n", serverURL)
						return serverURL, nil
					}
				}
			}
		}
	}

	// No running server found, start one if requested
	if startServer {
		// Find a free port
		freePort, err := testutils.FindFreePort(11002)
		if err != nil {
			return "", fmt.Errorf("failed to find free port: %w", err)
		}

		fmt.Printf("Starting test server on port %d...\n", freePort)
		server, err := testutils.StartTestServer(freePort, "")
		if err != nil {
			return "", fmt.Errorf("failed to start test server: %w", err)
		}

		fmt.Printf("✓ Test server started on port %d\n", server.Port)
		return fmt.Sprintf("http://localhost:%d", server.Port), nil
	}

	return "", fmt.Errorf("no running test server found and -start-server=false")
}

func stopFUSE(pidFile string) error {
	if _, err := os.Stat(pidFile); os.IsNotExist(err) {
		return fmt.Errorf("PID file not found: %s", pidFile)
	}

	pidData, err := os.ReadFile(pidFile)
	if err != nil {
		return fmt.Errorf("failed to read PID file: %w", err)
	}

	var pid int
	if _, err := fmt.Sscanf(string(pidData), "%d", &pid); err != nil {
		return fmt.Errorf("invalid PID in file: %w", err)
	}

	// Extract mount point from PID file path
	mountPoint := fmt.Sprintf("%s", pidFile[:len(pidFile)-9]) // Remove "/fuse.pid"
	fmt.Printf("Unmounting %s...\n", mountPoint)
	if err := testutils.UnmountFUSE(mountPoint); err != nil {
		fmt.Printf("Warning: failed to unmount: %v\n", err)
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find process: %w", err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to signal process: %w", err)
	}

	fmt.Printf("Sent SIGTERM to process %d, waiting for shutdown...\n", pid)

	// Wait a moment for graceful shutdown
	time.Sleep(2 * time.Second)

	// Check if it's still running and force kill if needed
	if err := process.Signal(syscall.Signal(0)); err == nil {
		fmt.Printf("Process still running, force killing...\n")
		if err := process.Kill(); err != nil {
			return fmt.Errorf("failed to kill process: %w", err)
		}
	}

	os.Remove(pidFile)
	fmt.Printf("FUSE mount stopped\n")
	return nil
}