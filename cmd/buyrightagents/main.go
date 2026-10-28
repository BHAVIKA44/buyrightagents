package main

import (
	"fmt"
	"os"
)

const version = "0.1.0"

func run(args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(os.Stdout, "BuyRightAgents: purchase requirement verification prototype")
		fmt.Fprintln(os.Stdout, "Usage: buyrightagents [help|version]")
		return 0
	}

	switch args[0] {
	case "version", "--version", "-v":
		fmt.Fprintf(os.Stdout, "BuyRightAgents %s\n", version)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\nRun 'buyrightagents help' for usage.\n", args[0])
		return 2
	}
}

func main() {
	os.Exit(run(os.Args[1:]))
}
