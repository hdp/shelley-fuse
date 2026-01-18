package testutils

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"
)

// TestServer represents a running Shelley test server
type TestServer struct {
	Port     int
	DBPath   string
	PID      int
	Cmd      *exec.Cmd
	LogPath  string
}

// StartTestServer starts a predictable-only Shelley server for testing
func StartTestServer(port int, dbDir string) (*TestServer, error) {
	if dbDir == "" {
		dbDir = fmt.Sprintf("/tmp/shelley-test-db-%d", port)
	}
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db dir: %w", err)
	}

	dbPath := fmt.Sprintf("%s/test.db", dbDir)
	logPath := fmt.Sprintf("%s/server.log", dbDir)
	pidFile := fmt.Sprintf("/tmp/shelley-server-%d.pid", port)

	// Check if server is already running
	if _, err := os.Stat(pidFile); err == nil {
		pidData, err := os.ReadFile(pidFile)
		if err == nil {
			var pid int
			_, err = fmt.Sscanf(string(pidData), "%d", &pid)
			if err == nil {
				// Check if process is still running
				if process, err := os.FindProcess(pid); err == nil {
					if err := process.Signal(os.Signal(nil)); err == nil {
						return &TestServer{
							Port:    port,
							DBPath:  dbPath,
							PID:     pid,
							LogPath: logPath,
						}, nil
					}
				}
			}
		}
		// Remove stale PID file
		os.Remove(pidFile)
	}

	// Start shelley server
	cmd := exec.Command("/usr/local/bin/shelley",
		"-db", dbPath,
		"-predictable-only",
		"serve",
		"-port", fmt.Sprintf("%d", port),
		"-require-header", "X-Exedev-Userid")

	// Clear environment variables that might interfere with testing
	// (consistent with integration_test.go)
	cmd.Env = append(os.Environ(),
		"FIREWORKS_API_KEY=",
		"ANTHROPIC_API_KEY=",
		"OPENAI_API_KEY=",
	)

	// Redirect output to log file
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create log file: %w", err)
	}
	defer logFile.Close()

	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start shelley server: %w", err)
	}

	// Write PID file
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644); err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("failed to write PID file: %w", err)
	}

	// Wait for server to be ready
	if err := WaitForServer(fmt.Sprintf("http://localhost:%d", port), 10*time.Second); err != nil {
		cmd.Process.Kill()
		os.Remove(pidFile)
		return nil, fmt.Errorf("server failed to start: %w", err)
	}

	return &TestServer{
		Port:    port,
		DBPath:  dbPath,
		PID:     cmd.Process.Pid,
		Cmd:     cmd,
		LogPath: logPath,
	}, nil
}

// Stop stops the test server
func (ts *TestServer) Stop() error {
	pidFile := fmt.Sprintf("/tmp/shelley-server-%d.pid", ts.Port)
	defer os.Remove(pidFile)

	if ts.Cmd != nil && ts.Cmd.Process != nil {
		if err := ts.Cmd.Process.Kill(); err != nil {
			return fmt.Errorf("failed to kill process: %w", err)
		}
		ts.Cmd.Wait()
	}
	return nil
}

// WaitForServer waits for a server to respond successfully
// Copied from shelley/integration_test.go for reuse
func WaitForServer(url string, timeout time.Duration) error {
	client := &http.Client{}
	timeoutChan := time.After(timeout)
	tick := time.Tick(100 * time.Millisecond)

	for {
		select {
		case <-timeoutChan:
			return fmt.Errorf("timeout waiting for server at %s", url)
		case <-tick:
			resp, err := client.Get(url)
			if err == nil && resp.StatusCode == http.StatusOK {
				resp.Body.Close()
				return nil
			}
			if resp != nil {
				resp.Body.Close()
			}
		}
	}
}

// FindFreePort finds an available port starting from the given port
func FindFreePort(startPort int) (int, error) {
	port := startPort
	for port < startPort+100 { // Try up to 100 ports
		conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", port))
		if err != nil {
			// Port is available
			return port, nil
		}
		conn.Close()
		port++
	}
	return 0, fmt.Errorf("no free ports found starting from %d", startPort)
}