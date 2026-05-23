package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: probe-env-read <name>")
		os.Exit(2)
	}

	name := os.Args[1]
	value, ok := os.LookupEnv(name)
	if !ok {
		fmt.Fprintf(os.Stderr, "env-missing name=%s\n", name)
		os.Exit(1)
	}

	fmt.Printf("env-ok name=%s value=%s\n", name, value)
}
