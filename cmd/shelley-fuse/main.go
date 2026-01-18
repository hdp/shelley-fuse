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
)

func main() {
	// Parse command line arguments
	server := flag.Bool("server", false, "run as server")
	debug := flag.Bool("debug", false, "enable debug output")
	flag.Parse()

	if flag.NArg() < 2 {
		fmt.Printf("Usage: %s [options] MOUNTPOINT URL\n", os.Args[0])
		fmt.Printf("Options:\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	mountpoint := flag.Arg(0)
	url := flag.Arg(1)

	// Create Shelley client
	client := shelley.NewClient(url)

	// Create FUSE filesystem
	shelleyFS := shelleyfuse.NewFS(client)

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

	// Run the server
	if *server {
		fssrv.Wait()
	} else {
		fssrv.Wait()
	}
}