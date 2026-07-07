// mst — Meta-Search Tool.
//
// A command-line meta-search tool that fans out concurrent queries to multiple
// search engines, merges and re-ranks the results, and emits them as JSON.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/earendil-works/meta-search-tool/internal/cli"
	"github.com/earendil-works/meta-search-tool/internal/config"
)

const version = "dev"

func main() {
	if err := run(); err != nil {
		code := exitCode(err)
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(code)
	}
}

// exitCode returns the appropriate exit code for an error.
func exitCode(err error) int {
	switch err.(type) {
	case *UsageError:
		return 2
	case *ConfigError:
		return 2
	default:
		return 1
	}
}

// UsageError indicates a CLI usage / flag error.
type UsageError struct {
	Message string
}

func (e *UsageError) Error() string { return e.Message }

// ConfigError indicates a config file error.
type ConfigError struct {
	Message string
}

func (e *ConfigError) Error() string { return e.Message }

func run() error {
	// Parse CLI flags.
	flags, err := cli.Parse()
	if err != nil {
		return &UsageError{Message: err.Error()}
	}

	// Short-circuit: --version.
	if flags.Version {
		fmt.Printf("mst %s\n", version)
		return nil
	}

	// Load config.
	cfg, err := config.Load()
	if err != nil {
		return &ConfigError{Message: err.Error()}
	}

	// --list-engines short-circuits before query resolution.
	if flags.ListEngines {
		fmt.Println("engines: (registry empty — add engines in Stage 1)")
		return nil
	}

	// Resolve query.
	query, err := resolveQuery(flags)
	if err != nil {
		return &UsageError{Message: err.Error()}
	}
	flags.Query = query

	// Validate format.
	if err := validateFormat(flags); err != nil {
		return &UsageError{Message: err.Error()}
	}

	fmt.Printf("query=%s format=%s limit=%d engines=%v\n",
		flags.Query, flags.Format, flags.Limit, flags.Engines)

	// TODO: engine registry, dispatcher, ranker, formatter
	_ = cfg

	return nil
}

// resolveQuery reads from stdin if --stdin is set and no positional args given.
func resolveQuery(flags *cli.Flags) (string, error) {
	if flags.Query != "" {
		return flags.Query, nil
	}

	if flags.Stdin {
		data, err := readStdin()
		if err != nil {
			return "", fmt.Errorf("stdin error: %w", err)
		}
		q := strings.TrimSpace(data)
		if q == "" {
			return "", &UsageError{Message: "empty query from stdin"}
		}
		return q, nil
	}

	return "", &UsageError{Message: "no query provided (use positional args or --stdin)"}
}

// readStdin reads all data from stdin.
func readStdin() (string, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("reading stdin: %w", err)
	}
	return string(data), nil
}

// validateFormat checks format compatibility.
func validateFormat(flags *cli.Flags) error {
	if flags.Raw && flags.Format != "json" {
		return &UsageError{Message: fmt.Sprintf("--raw is only compatible with --format json, got %s", flags.Format)}
	}
	return nil
}
