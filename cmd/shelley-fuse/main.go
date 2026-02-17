package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
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

// SocketInfo represents a socket entry from systemctl list-sockets --output=json.
type SocketInfo struct {
	Listen    string `json:"listen"`
	Unit      string `json:"unit"`
	Activates string `json:"activates"`
}

// parseListenAddress parses the JSON output from `systemctl list-sockets shelley.socket --output=json`
// and returns an HTTP URL for the first TCP listen address found.
func parseListenAddress(jsonOutput string) (string, error) {
	var sockets []SocketInfo
	if err := json.Unmarshal([]byte(jsonOutput), &sockets); err != nil {
		return "", fmt.Errorf("failed to parse systemctl JSON output: %w", err)
	}

	// The output should contain shelley.socket entries
	for _, s := range sockets {
		// Skip unix sockets (absolute paths)
		if strings.HasPrefix(s.Listen, "/") {
			continue
		}

		// Parse as a TCP address
		host, port, err := net.SplitHostPort(s.Listen)
		if err != nil {
			continue
		}

		// Replace wildcard/unspecified addresses with localhost
		if host == "" || host == "0.0.0.0" || host == "::" {
			host = "localhost"
		}

		return fmt.Sprintf("http://%s", net.JoinHostPort(host, port)), nil
	}

	return "", fmt.Errorf("no TCP listen address found for shelley.socket")
}

// discoverBackendURL attempts to discover the backend URL from the
// shelley.socket systemd unit using systemctl's JSON output format.
// Falls back to defaultBackendURL on failure.
func discoverBackendURL() string {
	out, err := exec.Command("systemctl", "list-sockets", "shelley.socket", "--output=json").Output()
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
	statePath := flag.String("state", "", "path to state.json (default: ~/.shelley-fuse/state.json)")
	readyFD := flag.Int("ready-fd", 0, "fd number; when >0, write READY\\n to this fd after mount+diag are ready, then close it")
	diagAddr := flag.String("diag-addr", "", "address for diag HTTP server (default: disabled)")
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
	store, err := state.NewStore(*statePath)
	if err != nil {
		log.Fatalf("Failed to initialize state: %v", err)
	}

	// Set the URL for the default backend (creating it if needed)
	if err := store.EnsureBackendURL(state.DefaultBackendName, url); err != nil {
		log.Fatalf("Failed to set backend URL: %v", err)
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

	// Start diag HTTP server if requested.
	if *diagAddr != "" {
		diagListener, err := net.Listen("tcp", *diagAddr)
		if err != nil {
			log.Fatalf("Failed to listen for diag server on %s: %v", *diagAddr, err)
		}
		diagMux := http.NewServeMux()
		diagMux.Handle("/diag", shelleyFS.Diag.Handler())
		diagSrv := &http.Server{Handler: diagMux}
		go diagSrv.Serve(diagListener)
		fmt.Fprintf(os.Stderr, "DIAG=http://%s/diag\n", diagListener.Addr().String())
	}

	// Signal readiness via the ready-fd pipe if requested.
	if *readyFD > 0 {
		f := os.NewFile(uintptr(*readyFD), "ready-fd")
		if f == nil {
			log.Fatalf("Invalid ready-fd %d", *readyFD)
		}
		if _, err := f.WriteString("READY\n"); err != nil {
			log.Fatalf("Failed to write to ready-fd: %v", err)
		}
		f.Close()
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
