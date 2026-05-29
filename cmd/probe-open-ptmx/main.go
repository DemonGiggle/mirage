package main

import (
	"fmt"
	"os"
)

func main() {
	file, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open-failed path=/dev/ptmx err=%v\n", err)
		os.Exit(1)
	}
	if err := file.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "close-failed path=/dev/ptmx err=%v\n", err)
		os.Exit(1)
	}
	fmt.Println("open-ok path=/dev/ptmx")
}
