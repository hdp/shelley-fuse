# Shelley FUSE Justfile
# Convenient commands for testing and development

# Default target
list:
    just --list

# Build all binaries
build:
    go build -o shelley-fuse ./cmd/shelley-fuse
    cd tools && go build -o bin/start-test-server ./start-test-server && go build -o bin/start-fuse ./start-fuse

# Build just the FUSE binary
build-fuse:
    go build -o shelley-fuse ./cmd/shelley-fuse

# Build just the test tools
build-tools:
    cd tools && go build -o bin/start-test-server ./start-test-server && go build -o bin/start-fuse ./start-fuse

# Start test server
start-server port="11002":
    cd tools && ./bin/start-test-server -port {{port}}

# Start FUSE with auto server management
# Use 'just fuse-port' if you need to specify a server
fuse mount="/tmp/shelley-fuse":
    cd tools && ./bin/start-fuse -mount {{mount}}

# Start FUSE using specific server port
fuse-port mount="/tmp/shelley-fuse" port="11002":
    cd tools && ./bin/start-fuse -mount {{mount}} -server-port {{port}}

# Quick test: start server and FUSE together
test mount="/tmp/shelley-fuse" port="11002":
    echo "Starting Shelley FUSE test environment..."
    echo "Mount point: {{mount}}"
    echo "Server port: {{port}}"
    echo ""
    # Start server in background
    cd tools && ./bin/start-test-server -port {{port}} &
    sleep 2
    # Start FUSE
    cd tools && ./bin/start-fuse -mount {{mount}} -server-port {{port}}

# Start everything and open a shell for testing
test-shell mount="/tmp/shelley-fuse" port="11002":
    echo "Starting test environment..."
    echo "Mount point: {{mount}}"
    echo "Server port: {{port}}"
    echo ""
    # Start server
    just start-server {{port}} &
    sleep 2
    # Start FUSE
    just fuse-port {{mount}} {{port}}

# Stop server
stop-server port="11002":
    cd tools && ./bin/start-test-server -stop -port {{port}}

# Stop FUSE
stop-fuse mount="/tmp/shelley-fuse":
    cd tools && ./bin/start-fuse -stop -mount {{mount}}

# Stop all test services
stop mount="/tmp/shelley-fuse" port="11002":
    just stop-fuse {{mount}} || true
    just stop-server {{port}} || true
    echo "All test services stopped"

# Run unit tests
test-unit:
    go test ./...

# Run integration tests
test-integration:
    go test -v ./shelley -run TestIntegration

# Run all tests
test-all:
    just test-unit
    just test-integration

# Clean up build artifacts
clean:
    rm -f shelley-fuse
    rm -rf tools/bin
    rm -rf /tmp/shelley-fuse*
    rm -rf /tmp/shelley-test-db-*
    echo "Cleaned up build artifacts and test directories"

# Full development setup
dev mount="/tmp/shelley-fuse" port="11002":
    just build
    just clean
    just test-shell {{mount}} {{port}}

# Show test environment status
status mount="/tmp/shelley-fuse" port="11002":
    #!/bin/bash
    echo "=== Test Environment Status ==="
    echo "Mount point: {{mount}}"
    echo "Server port: {{port}}"
    echo ""
    # Check server
    if curl -s -H "X-Exedev-Userid: 1" http://localhost:{{port}} >/dev/null 2>&1; then
        echo "✓ Test server running on port {{port}}"
    else
        echo "✗ Test server not running on port {{port}}"
    fi
    # Check FUSE mount
    if [ -d "{{mount}}/default" ]; then
        echo "✓ FUSE filesystem mounted at {{mount}}"
        echo "  Contents:"
        ls -la {{mount}}/default/ 2>/dev/null | head -5 || echo "  (error listing contents)"
    else
        echo "✗ FUSE filesystem not mounted at {{mount}}"
    fi

# Quick demo using default settings
demo:
    just test-shell

# Build release version
release:
    go build -ldflags "-s -w" -o shelley-fuse ./cmd/shelley-fuse
    cd tools && go build -ldflags "-s -w" -o bin/start-test-server ./start-test-server && go build -ldflags "-s -w" -o bin/start-fuse ./start-fuse
    echo "Release binaries built"