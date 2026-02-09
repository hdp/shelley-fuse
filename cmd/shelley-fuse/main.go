package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	shelleyfuse "shelley-fuse/fuse"
	"shelley-fuse/shelley"
	"shelley-fuse/state"
)

const defaultBackendURL = "http://localhost:9999"

// parseListenAddress parses the output of `systemctl show shelley.socket -p Listen`
// and returns an HTTP URL for the first TCP listen address found.
//
// Expected input formats:
//
//	Listen=127.0.0.1:9999 (Stream)
//	Listen=[::]:9999 (Stream)
//	Listen=0.0.0.0:8080 (Stream)
//	Listen=/run/shelley.sock (Stream)
func parseListenAddress(output string) (string, error) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Strip "Listen=" prefix if present
		line = strings.TrimPrefix(line, "Listen=")

		// Strip the trailing " (Stream)", " (Datagram)", etc.
		if idx := strings.LastIndex(line, " ("); idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Skip unix sockets (absolute paths)
		if strings.HasPrefix(line, "/") {
			continue
		}

		// Parse as a TCP address
		host, port, err := net.SplitHostPort(line)
		if err != nil {
			continue
		}

		// Replace wildcard/unspecified addresses with localhost
		if host == "" || host == "0.0.0.0" || host == "::" {
			host = "localhost"
		}

		return fmt.Sprintf("http://%s", net.JoinHostPort(host, port)), nil
	}

	return "", fmt.Errorf("no TCP listen address found in systemctl output")
}

// discoverBackendURL attempts to discover the backend URL from the
// shelley.socket systemd unit. Falls back to defaultBackendURL on failure.
func discoverBackendURL() string {
	out, err := exec.Command("systemctl", "show", "shelley.socket", "-p", "Listen").Output()
	if err != nil {
		log.Printf("Failed to query shelley.socket: %v; using default %s", err, defaultBackendURL)
		return defaultBackendURL
	}

	url, err := parseListenAddress(string(out))
	if err != nil {
		log.Printf("Failed to parse shelley.socket listen address: %v; using default %s", err, defaultBackendURL)
		return defaultBackendURL
	}

	return url
}

func main() {
	debug := flag.Bool("debug", false, "enable debug output")
	cloneTimeout := flag.Duration("clone-timeout", time.Hour, "duration after which unconversed clone IDs are cleaned up")
	cacheTTL := flag.Duration("cache-ttl", 3*time.Second, "cache TTL for backend responses (0 to disable caching)")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Printf("Usage: %s [options] MOUNTPOINT [URL]\n", os.Args[0])
		fmt.Printf("Options:\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	mountpoint := flag.Arg(0)

	var url string
	if flag.NArg() >= 2 {
		url = flag.Arg(1)
	} else {
		url = discoverBackendURL()
	}
	log.Printf("Using backend URL: %s", url)

	// Create Shelley client with optional caching
	baseClient := shelley.NewClient(url)
	var client shelley.ShelleyClient
	if *cacheTTL > 0 {
		client = shelley.NewCachingClient(baseClient, *cacheTTL)
	} else {
		client = baseClient
	}

	// Create state store
	store, err := state.NewStore("")
	if err != nil {
		log.Fatalf("Failed to initialize state: %v", err)
	}

	// Create FUSE filesystem
	shelleyFS := shelleyfuse.NewFS(client, store, *cloneTimeout)

	// Set up FUSE server options
	opts := &fs.Options{}
	opts.Debug = *debug
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	// Mount the filesystem
	fssrv, err := fs.Mount(mountpoint, shelleyFS, opts)
	if err != nil {
		log.Fatalf("Mount failed: %v", err)
	}

	// Set up signal handling for clean unmount
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signals
		fssrv.Unmount()
		os.Exit(0)
	}()

	fssrv.Wait()
}
