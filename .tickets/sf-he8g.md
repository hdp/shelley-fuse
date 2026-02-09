---
id: sf-he8g
status: closed
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


## Notes

**2026-02-09T02:21:16Z**

Socket unit: A systemd .socket unit for shelley-fuse itself doesn't apply — FUSE filesystems aren't network services. Socket activation for FUSE mounts (via /dev/fuse fd passing) is possible in theory but go-fuse/v2 doesn't support receiving fds from systemd. Instead, the service unit uses Requires=shelley.socket to depend on the shelley backend socket. Omitting a shelley-fuse.socket file.
