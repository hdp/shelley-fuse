package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	shelleyfuse "shelley-fuse/fuse"
	"shelley-fuse/shelley"
	"shelley-fuse/state"
)

func main() {
	debug := flag.Bool("debug", false, "enable debug output")
	cloneTimeout := flag.Duration("clone-timeout", time.Hour, "duration after which unconversed clone IDs are cleaned up")
	cacheTTL := flag.Duration("cache-ttl", 3*time.Second, "cache TTL for backend responses (0 to disable caching)")
	flag.Parse()

	if flag.NArg() < 2 {
		fmt.Printf("Usage: %s [options] MOUNTPOINT URL\n", os.Args[0])
		fmt.Printf("Options:\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	mountpoint := flag.Arg(0)
	url := flag.Arg(1)

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
