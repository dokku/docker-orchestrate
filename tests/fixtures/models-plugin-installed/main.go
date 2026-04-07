// Mock docker-model CLI plugin for integration testing.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: docker-model <command>")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "docker-cli-plugin-metadata":
		fmt.Println(`{"SchemaVersion":"0.1.0","Vendor":"Docker Inc.","Version":"0.0.1-test","ShortDescription":"Mock Model Runner"}`)
	case "version":
		fmt.Println("Docker Model Runner version 0.0.1-test")
	case "ls":
		fmt.Println("[]")
	case "pull":
		if len(os.Args) > 2 {
			fmt.Printf("Pulling model %s...\nDone\n", os.Args[2])
		}
	case "configure":
		// Accept any flags silently
	case "status":
		fmt.Println(`{"endpoint":"http://localhost:12434"}`)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}
