package main

import (
	"fmt"
	"os"
)

// Version is set at build time via -ldflags.-X main.Version=...
var Version = "dev"

func main() {
	if err := NewRootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
