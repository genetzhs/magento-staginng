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
	// Resolve the subcommand. We check for the special "help/version/
	// check-update" cases first so they bypass the automatic update check.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "-v", "--version":
			showVersion()
			return
		case "check-update":
			// Explicit check: always hits GitHub, exits when done.
			checkGitHubUpdate()
			return
		case "--check-update":
			// Backwards compat: old flag form
			checkGitHubUpdate()
			return
		case "help", "-h", "--help":
			showHelp()
			return
		}
	}

	// For real subcommands, run a (cached, non-blocking) update check first.
	// It writes at most a one-line notice to stderr and never fails the run.
	maybeCheckUpdate()

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
