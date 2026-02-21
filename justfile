# Shelley FUSE Justfile

# Default: list available commands
list:
    just --list

# Build the FUSE binary
build binary="./shelley-fuse":
    go build -o {{binary}} ./cmd/shelley-fuse

# Run all tests
test:
    go test ./...

# Start shelley-fuse for manual testing (Ctrl+C to stop and unmount)
dev mount="~/mnt/shelley" url="http://localhost:9999":
    just build
    just run-dev {{mount}} {{url}}

run-dev mount="~/mnt/shelley" url="http://localhost:9999":
    @mkdir -p {{mount}}
    ./shelley-fuse {{mount}} {{url}}

# Start shelley-fuse for manual testing with autoreload
dev-reload:
    bash scripts/dev-reload 

# Install shelley-fuse: build, install binary, install systemd unit, enable and start
install:
    just build
    sudo install -m 755 shelley-fuse /usr/local/bin/shelley-fuse
    sudo cp shelley-fuse.service /etc/systemd/system/shelley-fuse.service
    sudo systemctl daemon-reload
    sudo systemctl enable shelley-fuse.service
    sudo systemctl restart shelley-fuse.service
    @echo "shelley-fuse installed and started"
    @echo "Mount point: /shelley"
    @echo "Check status: systemctl status shelley-fuse"

# Clean build artifacts
clean:
    rm -f shelley-fuse
