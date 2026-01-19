package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"shelley-fuse/testhelper"
	"syscall"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Printf("Usage: %s [command] [options]\n", os.Args[0])
		fmt.Printf("Commands:\n")
		fmt.Printf("  server    Start test Shelley server\n")
		fmt.Printf("  fuse      Start test FUSE mount\n")
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "server":
		serverCommand(args)
	case "fuse":
		fuseCommand(args)
	default:
		log.Fatalf("Unknown command: %s", cmd)
	}
}

func serverCommand(args []string) {
	flags := flag.NewFlagSet("server", flag.ExitOnError)
	port := flags.Int("port", 11002, "Port for server")

	if err := flags.Parse(args); err != nil {
		log.Fatalf("Error parsing flags: %v", err)
	}

	server, err := testhelper.StartTestServer(*port, "")
	if err != nil {
		log.Fatalf("Failed to start test server: %v", err)
	}

	fmt.Printf("✓ Shelley test server started\n")
	fmt.Printf("  Server URL: http://localhost:%d\n", server.Port)
	fmt.Printf("  Database: %s\n", server.DBPath)

	// Set up signal handling for clean shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	fmt.Printf("\nPress Ctrl+C to stop server...\n")
	<-c

	fmt.Printf("\nShutting down server...\n")
	if err := server.Stop(); err != nil {
		log.Printf("Error stopping server: %v", err)
	}
	fmt.Printf("Server stopped\n")
}

func fuseCommand(args []string) {
	flags := flag.NewFlagSet("fuse", flag.ExitOnError)
	mount := flags.String("mount", "/tmp/shelley-fuse-test", "Mount point")
	server := flags.String("server", "http://localhost:11002", "Shelley server URL")
	inProcess := flags.Bool("in-process", false, "Run FUSE server in-process for better error reporting")

	if err := flags.Parse(args); err != nil {
		log.Fatalf("Error parsing flags: %v", err)
	}

	var mountObj *testhelper.FUSEMount
	var err error

	if *inProcess {
		// For the testhelper binary, we'll just show an error since we don't have access to the FUSE package
		fmt.Println("In-process mode requires a function to create the filesystem")
		fmt.Println("This mode is intended for use in tests, not the testhelper binary")
		os.Exit(1)
	} else {
		mountObj, err = testhelper.StartFUSE(*mount, *server)
	}
	
	if err != nil {
		log.Fatalf("Failed to start FUSE mount: %v", err)
	}

	fmt.Printf("✓ FUSE mount started\n")
	fmt.Printf("  Mount point: %s\n", mountObj.MountPoint)
	if *inProcess {
		fmt.Printf("  Mode: in-process\n")
	} else {
		fmt.Printf("  Mode: external process\n")
	}

	// Set up signal handling for clean shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	fmt.Printf("\nPress Ctrl+C to stop mount...\n")
	<-c

	fmt.Printf("\nShutting down FUSE mount...\n")
	if err := mountObj.Stop(); err != nil {
		log.Printf("Error stopping FUSE: %v", err)
	}
	fmt.Printf("FUSE mount stopped\n")
}