package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: probe-file-read <path>")
		os.Exit(2)
	}

	path := os.Args[1]
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read-failed path=%s err=%v\n", path, err)
		os.Exit(1)
	}

	fmt.Printf("read-ok path=%s bytes=%d\n", path, len(data))
}
