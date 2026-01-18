#!/bin/bash

echo "Building Shelley FUSE..."
go build -o shelley-fuse ./cmd/shelley-fuse

echo "Running tests..."
go test ./...

echo "Build complete!"
echo "Usage: ./shelley-fuse MOUNTPOINT URL"
echo "Example: ./shelley-fuse /mnt/shelley http://localhost:9999"