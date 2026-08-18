package main

import (
	"os"
)

// version is set at build time via -ldflags.
var (
	version = "dev"
	commit  = "none"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "create":
			runCreate(os.Args[2:])
			return
		case "list":
			runList(os.Args[2:])
			return
		case "info":
			runInfo(os.Args[2:])
			return
		case "cleanup":
			runCleanup(os.Args[2:])
			return
		}
	}

	// No subcommand or unknown -> show help
	showHelp()
}
