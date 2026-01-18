#!/bin/bash

# demo.sh - Simple demonstration of the new tools

set -e

echo "=== Shelley FUSE Tools Demo ==="
echo

# Start test server
echo "1. Starting test server..."
./bin/start-test-server &
SERVER_PID=$!
sleep 2

# Test FUSE mount
echo "2. Mounting FUSE filesystem..."
timeout 5s ./bin/start-fuse -mount /tmp/demo-fuse || true

# If mount succeeded, show filesystem content
if [ -d "/tmp/demo-fuse/default" ]; then
    echo "3. Filesystem mounted successfully:"
    ls -la /tmp/demo-fuse/default/
    echo
    echo "Models available:"
    cat /tmp/demo-fuse/default/models 2>/dev/null || echo "No models file found"
fi

# Cleanup
echo "4. Cleaning up..."
./bin/start-fuse -stop -mount /tmp/demo-fuse || true
./bin/start-test-server -stop -port 11002 || true

echo "Demo complete!"
