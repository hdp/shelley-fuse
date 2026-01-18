#!/bin/bash

# start-test-server.sh - Start a predictable-only Shelley server for testing
# Usage: ./start-test-server.sh [port] [db_dir]
# Defaults: port=11002, db_dir=/tmp/shelley-test-db-<port>

set -e

PORT=${1:-11002}
DB_DIR=${2:-/tmp/shelley-test-db-$PORT}
SERVER_PID_FILE="/tmp/shelley-server-$PORT.pid"
DB_PATH="$DB_DIR/test.db"

# Check if shelley binary exists
SHELLEY_BIN="/usr/local/bin/shelley"
if [ ! -f "$SHELLEY_BIN" ]; then
    echo "ERROR: Shelley binary not found at $SHELLEY_BIN"
    echo "Please ensure shelley is installed and available"
    exit 1
fi

echo "Starting Shelley test server..."
echo "Port: $PORT"
echo "Database: $DB_PATH"
echo "Mode: predictable-only"

# Create database directory
mkdir -p "$DB_DIR"

# Function to check if server is already running
is_server_running() {
    if [ -f "$SERVER_PID_FILE" ]; then
        PID=$(cat "$SERVER_PID_FILE")
        if kill -0 "$PID" 2>/dev/null; then
            echo "Server already running (PID: $PID)"
            return 0
        else
            # PID file exists but process is dead
            rm -f "$SERVER_PID_FILE"
        fi
    fi
    return 1
}

# Function to wait for server to be ready
wait_for_server() {
    local timeout=20
    echo "Waiting for server to start..."
    
    for i in $(seq 1 $timeout); do
        if curl -s -H "X-Exedev-Userid: 1" "http://localhost:$PORT/" >/dev/null 2>&1; then
            echo "Server is ready!"
            return 0
        fi
        if [ $i -eq 1 ]; then
            echo "Waiting... ($i/$timeout)"
        else
            echo -n "\rWaiting... ($i/$timeout)"
        fi
        sleep 0.5
    done
    
    echo ""
    echo "ERROR: Server failed to start within timeout"
    return 1
}

# Check if server is already running
if is_server_running; then
    echo "Server is already running on port $PORT"
    echo "Server URL: http://localhost:$PORT"
    exit 0
fi

echo "Starting Shelley server..."

# Start server in predictable-only mode with clean environment
# Clear potentially interfering API keys (like in integration_test.go)
env -i PATH="$PATH" HOME="$HOME" USER="$USER" \
    "$SHELLEY_BIN" \
    -db "$DB_PATH" \
    -predictable-only \
    serve \
    -port "$PORT" \
    -require-header X-Exedev-Userid \
    > "$DB_DIR/server.log" 2>&1 &

SERVER_PID=$!
echo "$SERVER_PID" > "$SERVER_PID_FILE"

echo "Started server with PID: $SERVER_PID"

# Verify server started successfully
if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    echo "ERROR: Server process died immediately"
    echo "Log output:"
    cat "$DB_DIR/server.log" 2>/dev/null || echo "No log file found"
    rm -f "$SERVER_PID_FILE"
    exit 1
fi

# Wait for server to be ready
if ! wait_for_server; then
    echo "Server log:"
    cat "$DB_DIR/server.log" 2>/dev/null || echo "No log file found"
    # Clean up
    kill "$SERVER_PID" 2>/dev/null || true
    rm -f "$SERVER_PID_FILE"
    exit 1
fi

echo ""
echo "✓ Shelley test server started successfully!"
echo "Server URL: http://localhost:$PORT"
echo "Database: $DB_PATH"
echo "Log file: $DB_DIR/server.log"
echo "PID file: $SERVER_PID_FILE"
echo ""
echo "To stop the server:"
echo "  kill \\$(cat $SERVER_PID_FILE)"
echo "  # or use: $0 --stop $PORT"

# Handle stop command
if [ "$1" = "--stop" ]; then
    STOP_PORT=${2:-$PORT}
    STOP_PID_FILE="/tmp/shelley-server-$STOP_PORT.pid"
    
    if [ -f "$STOP_PID_FILE" ]; then
        PID=$(cat "$STOP_PID_FILE")
        if kill -0 "$PID" 2>/dev/null; then
            echo "Stopping server (PID: $PID)..."
            kill "$PID"
            rm -f "$STOP_PID_FILE"
            echo "Server stopped"
        else
            echo "Server not running (stale PID file)"
            rm -f "$STOP_PID_FILE"
        fi
    else
        echo "No server running on port $STOP_PORT"
    fi
    exit 0
fi

# Keep server running in background, script exits
# Server will continue running until explicitly killed
