package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"shelley-fuse/tools/testutils"
)

func main() {
	var (
		port     = flag.Int("port", 11002, "Port to run server on")
		dbDir    = flag.String("db-dir", "", "Database directory (auto-generated if not specified)")
		stop     = flag.Bool("stop", false, "Stop server running on given port")
		pidFile  = flag.String("pid-file", "", "PID file path (auto-generated if not specified)")
	)
	flag.Parse()

	if *stop {
		if *pidFile == "" {
			*pidFile = fmt.Sprintf("/tmp/shelley-server-%d.pid", *port)
		}
		if err := stopServer(*pidFile); err != nil {
			log.Fatalf("Failed to stop server: %v", err)
		}
		return
	}

	// Start test server
	server, err := testutils.StartTestServer(*port, *dbDir)
	if err != nil {
		log.Fatalf("Failed to start test server: %v", err)
	}

	fmt.Printf("✓ Shelley test server started successfully!\n")
	fmt.Printf("Server URL: http://localhost:%d\n", server.Port)
	fmt.Printf("Database: %s\n", server.DBPath)
	fmt.Printf("Log file: %s\n", server.LogPath)
	fmt.Printf("\n")
	fmt.Printf("To stop the server, run:\n")
	fmt.Printf("  %s -stop -port %d\n", os.Args[0], *port)
	fmt.Printf("\n")

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Wait for interrupt signal
	<-sigChan
	fmt.Printf("\nShutting down server...\n")

	if err := server.Stop(); err != nil {
		log.Printf("Error stopping server: %v", err)
	} else {
		fmt.Printf("Server stopped\n")
	}
}

func stopServer(pidFile string) error {
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
	fmt.Printf("Server stopped\n")
	return nil
}