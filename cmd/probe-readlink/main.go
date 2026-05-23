package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: probe-readlink <path>")
		os.Exit(2)
	}

	path := os.Args[1]
	target, err := os.Readlink(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "readlink-failed path=%s err=%v\n", path, err)
		os.Exit(1)
	}

	fmt.Printf("readlink-ok path=%s target=%s\n", path, target)
}
