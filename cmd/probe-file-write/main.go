package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 || len(os.Args) > 3 {
		fmt.Fprintln(os.Stderr, "usage: probe-file-write <path> [content]")
		os.Exit(2)
	}

	path := os.Args[1]
	content := "probe-write\n"
	if len(os.Args) == 3 {
		content = os.Args[2]
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write-failed path=%s err=%v\n", path, err)
		os.Exit(1)
	}

	fmt.Printf("write-ok path=%s bytes=%d\n", path, len(content))
}
