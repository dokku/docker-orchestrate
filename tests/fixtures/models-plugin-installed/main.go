// Mock docker-model CLI plugin for integration testing.
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// PluginMetadata is the metadata for the plugin
type PluginMetadata struct {
	// SchemaVersion is the schema version of the plugin
	SchemaVersion string `json:"SchemaVersion"`
	// Vendor is the vendor of the plugin
	Vendor string `json:"Vendor"`
	// Version is the version of the plugin
	Version string `json:"Version"`
	// ShortDescription is the short description of the plugin
	ShortDescription string `json:"ShortDescription"`
}

func printMetadata() error {
	metadata := PluginMetadata{
		SchemaVersion:    "0.1.0",
		Vendor:           "Jose Diaz-Gonzalez",
		Version:          "v0.1.0",
		ShortDescription: "Mock Model Runner",
	}

	jsonData, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("error marshaling metadata: %v", err)
	}
	fmt.Println(string(jsonData))
	return nil
}

func main() {
	// remove `model` from arguments but keep the first argument and everything after it
	// this ensures we handle plugin registration (which calls it via `docker-model`)
	// and also handles the `docker model` command itself
	if os.Args[1] == "model" {
		os.Args = append(os.Args[:1], os.Args[2:]...)
	}

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: docker model <command>")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "docker-cli-plugin-metadata":
		if err := printMetadata(); err != nil {
			fmt.Fprintf(os.Stderr, "Error printing metadata: %v\n", err)
			os.Exit(1)
		}
	case "version":
		fmt.Println("Docker Model Runner version v0.1.0")
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
