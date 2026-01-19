package testhelper

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
	Cmd      *exec.Cmd
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

	// Start shelley server
	cmd := exec.Command("/usr/local/bin/shelley",
		"-db", dbPath,
		"-predictable-only",
		"serve",
		"-port", fmt.Sprintf("%d", port),
		"-require-header", "X-Exedev-Userid")

	// Clear environment variables that might interfere with testing
	cmd.Env = append(os.Environ(),
		"FIREWORKS_API_KEY=",
		"ANTHROPIC_API_KEY=",
		"OPENAI_API_KEY=",
	)

	// Start the process
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start shelley server: %w", err)
	}

	// Wait for server to be ready
	if err := WaitForServer(fmt.Sprintf("http://localhost:%d", port), 10*time.Second); err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("server failed to start: %w", err)
	}

	return &TestServer{
		Port:   port,
		DBPath: dbPath,
		Cmd:    cmd,
	}, nil
}

// Stop stops the test server
func (ts *TestServer) Stop() error {
	if ts.Cmd != nil && ts.Cmd.Process != nil {
		if err := ts.Cmd.Process.Kill(); err != nil {
			return fmt.Errorf("failed to kill process: %w", err)
		}
		ts.Cmd.Wait()
	}
	return nil
}

// WaitForServer waits for a server to respond successfully
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