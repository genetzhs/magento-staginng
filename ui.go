package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// prompt asks the user a question and returns the response (trimmed).
// If def is non-empty, it is shown as the default in [brackets]; pressing
// Enter accepts the default.
func prompt(question, def string) string {
	reader := bufio.NewReader(os.Stdin)
	if def != "" {
		fmt.Fprintf(os.Stderr, "%s [%s]: ", question, def)
	} else {
		fmt.Fprintf(os.Stderr, "%s: ", question)
	}
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

// promptConfirm asks a yes/no question. Default is "no" unless defYes=true.
func promptConfirm(question string, defYes bool) bool {
	def := "y/N"
	if defYes {
		def = "Y/n"
	}
	reader := bufio.NewReader(os.Stdin)
	fmt.Fprintf(os.Stderr, "%s [%s]: ", question, def)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" {
		return defYes
	}
	return line == "y" || line == "yes"
}

// promptChoice prompts for a value with validation.
func promptValue(question, def string, validate func(string) error) string {
	for {
		val := prompt(question, def)
		if validate != nil {
			if err := validate(val); err != nil {
				fmt.Fprintf(os.Stderr, "  invalid: %v\n", err)
				continue
			}
		}
		return val
	}
}

// validateNonEmpty ensures the value is non-empty.
func validateNonEmpty(s string) error {
	if s == "" {
		return fmt.Errorf("value cannot be empty")
	}
	return nil
}

// validateDomain ensures the value looks like a domain (has a dot).
func validateDomain(s string) error {
	if s == "" {
		return fmt.Errorf("domain cannot be empty")
	}
	if !strings.Contains(s, ".") {
		return fmt.Errorf("domain must contain a dot (e.g. example.com)")
	}
	return nil
}
