#!/bin/bash

# test-fuse.sh - Helper script to test Shelley FUSE filesystem
# Usage: ./test-fuse.sh [mount_point] [port]
# Defaults: mount_point=/tmp/shelley-fuse-test, port=11002

set -e

MOUNT_POINT=${1:-/tmp/shelley-fuse-test}
PORT=${2:-11002}
DB_DIR="/tmp/shelley-fuse-db-$PORT"
SERVER_PID_FILE="/tmp/shelley-server-$PORT.pid"
FUSE_PID_FILE="/tmp/shelley-fuse-$PORT.pid"

echo "Starting Shelley FUSE test environment..."
echo "Mount point: $MOUNT_POINT"
echo "Server port: $PORT"
echo "Database dir: $DB_DIR"

# Cleanup function
cleanup() {
    echo "Cleaning up..."
    
    # Kill FUSE process if running
    if [ -f "$FUSE_PID_FILE" ]; then
        FUSE_PID=$(cat "$FUSE_PID_FILE")
        if kill -0 "$FUSE_PID" 2>/dev/null; then
            echo "Killing FUSE process ($FUSE_PID)"
            kill "$FUSE_PID" 2>/dev/null || true
            # Give it a moment to unmount
            sleep 1
        fi
        rm -f "$FUSE_PID_FILE"
    fi
    
    # Kill server process if running
    if [ -f "$SERVER_PID_FILE" ]; then
        SERVER_PID=$(cat "$SERVER_PID_FILE")
        if kill -0 "$SERVER_PID" 2>/dev/null; then
            echo "Killing Shelley server ($SERVER_PID)"
            kill "$SERVER_PID" 2>/dev/null || true
        fi
        rm -f "$SERVER_PID_FILE"
    fi
    
    # Unmount FUSE filesystem if still mounted
    if mountpoint -q "$MOUNT_POINT"; then
        echo "Unmounting FUSE filesystem"
        fusermount -u "$MOUNT_POINT" 2>/dev/null || true
    fi
    
    # Clean up directories
    rm -rf "$DB_DIR"
    
    echo "Cleanup complete"
}

# Set up cleanup trap
trap cleanup EXIT INT TERM

# Create directories
mkdir -p "$MOUNT_POINT"
mkdir -p "$DB_DIR"

# Start Shelley server in predictable-only mode
echo "Starting Shelley server on port $PORT..."
/usr/local/bin/shelley \
    -db "$DB_DIR/test.db" \
    -predictable-only \
    serve \
    -port "$PORT" \
    -require-header X-Exedev-Userid \
    > "$DB_DIR/server.log" 2>&1 &
SERVER_PID=$!
echo "$SERVER_PID" > "$SERVER_PID_FILE"

# Wait for server to start
echo "Waiting for server to start..."
for i in {1..20}; do
    if curl -s -H "X-Exedev-Userid: 1" "http://localhost:$PORT/" >/dev/null 2>&1; then
        echo "Server is ready!"
        break
    fi
    echo "Waiting... ($i/20)"
    sleep 0.5
done

# Check if server is actually running
if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    echo "ERROR: Shelley server failed to start"
    cat "$DB_DIR/server.log"
    exit 1
fi

# Build FUSE filesystem
echo "Building FUSE filesystem..."
go build -o shelley-fuse ./cmd/shelley-fuse

# Mount FUSE filesystem
echo "Mounting FUSE filesystem..."
./shelley-fuse "$MOUNT_POINT" "http://localhost:$PORT" > "$DB_DIR/fuse.log" 2>&1 &
FUSE_PID=$!
echo "$FUSE_PID" > "$FUSE_PID_FILE"

# Wait for mount to be ready
echo "Waiting for FUSE mount to be ready..."
for i in {1..20}; do
    if mountpoint -q "$MOUNT_POINT" && [ -d "$MOUNT_POINT/default" ]; then
        echo "FUSE filesystem is ready!"
        break
    fi
    echo "Waiting for mount... ($i/20)"
    sleep 0.5
done

# Check if FUSE is actually mounted
if ! mountpoint -q "$MOUNT_POINT"; then
    echo "ERROR: FUSE filesystem failed to mount"
    echo "Server log:"
    cat "$DB_DIR/server.log"
    echo "FUSE log:"
    cat "$DB_DIR/fuse.log"
    exit 1
fi

echo ""
echo "Shelley FUSE test environment is ready!"
echo "Mount point: $MOUNT_POINT"
echo "Server URL: http://localhost:$PORT"
echo ""
echo "Try these commands:"
echo "  ls $MOUNT_POINT/default/"
echo "  cat $MOUNT_POINT/default/models"
echo "  echo 'Hello, Shelley!' > $MOUNT_POINT/default/model/predictable/new/test"
echo ""
echo "Press Ctrl+C to stop"

# Wait indefinitely
wait
