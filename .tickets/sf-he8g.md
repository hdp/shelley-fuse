---
id: sf-he8g
status: open
deps: []
links: []
created: 2026-02-09T02:04:29Z
type: task
priority: 2
assignee: hdp
---
# Add systemd files and install justfile recipe

Create systemd service unit file and corresponding socket unit for shelley-fuse. Add a 'install' recipe to the justfile that:
1. Builds the binary
2. Installs it to /usr/local/bin
3. Installs systemd service/socket files to /etc/systemd/system/
4. Enables and starts the service using systemd

This will provide a proper installation method for deploying shelley-fuse as a system service.

