package cli

import (
	"fmt"
	"io"
	"strings"
)

type bundledNetworkPolicyFile struct {
	Path        string
	Description string
}

var bundledNetworkPolicyFiles = []bundledNetworkPolicyFile{
	{
		Path:        "examples/network-policies/allow-all.yaml",
		Description: "Allow all ingress and egress; uses host network namespace passthrough.",
	},
	{
		Path:        "examples/network-policies/offline.yaml",
		Description: "Deny ingress and egress except loopback; uses the isolated namespace backend.",
	},
	{
		Path:        "examples/network-policies/block-local-egress.yaml",
		Description: "Allow internet egress while denying common local/private ranges; uses the routed namespace backend.",
	},
}

func runNetworkPolicy(args []string, stdout, stderr io.Writer) error {
	_ = stderr
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		printNetworkPolicyHelp(stdout)
		return nil
	}
	if args[0] == "help" {
		return runNetworkPolicyHelp(args[1:], stdout)
	}
	switch args[0] {
	case "list":
		return runNetworkPolicyListFiles(args[1:], stdout)
	default:
		return fmt.Errorf("unknown network-policy subcommand %q", args[0])
	}
}

func runNetworkPolicyHelp(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		printNetworkPolicyHelp(stdout)
		return nil
	}
	switch args[0] {
	case "list":
		printNetworkPolicyListFilesHelp(stdout)
		return nil
	default:
		return fmt.Errorf("unknown network-policy help topic %q", args[0])
	}
}

func printNetworkPolicyHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `Inspect bundled network policy examples.

Usage:
  mirage network-policy <subcommand>

Subcommands:
  list        list bundled example network policy files and their intent

Examples:
  mirage network-policy list
`)
}

func runNetworkPolicyListFiles(args []string, stdout io.Writer) error {
	if containsHelpFlag(args) {
		printNetworkPolicyListFilesHelp(stdout)
		return nil
	}
	if len(args) > 0 {
		return fmt.Errorf("network-policy list does not accept positional arguments: %s", strings.Join(args, " "))
	}

	_, _ = fmt.Fprintln(stdout, "mirage network-policy list")
	for _, entry := range bundledNetworkPolicyFiles {
		_, _ = fmt.Fprintf(stdout, "- %s: %s\n", entry.Path, entry.Description)
	}
	return nil
}

func printNetworkPolicyListFilesHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `List bundled example network policy files.

Usage:
  mirage network-policy list

Examples:
  mirage network-policy list
`)
}
