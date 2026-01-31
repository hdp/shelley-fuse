# Shelley FUSE Justfile

# Default: list available commands
list:
    just --list

# Build the FUSE binary
build:
    go build -o shelley-fuse ./cmd/shelley-fuse

# Run all tests
test:
    go test ./...

# Run integration tests (requires /usr/local/bin/shelley and fusermount)
test-integration:
    go test -v ./fuse -run TestPlan9Flow -timeout 60s

# Start shelley-fuse for manual testing (Ctrl+C to stop and unmount)
dev mount="/shelley" url="http://localhost:9999":
    just build
    mkdir -p {{mount}}
    ./shelley-fuse {{mount}} {{url}}

# Clean build artifacts
clean:
    rm -f shelley-fuse
