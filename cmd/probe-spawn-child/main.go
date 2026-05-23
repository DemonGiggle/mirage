package main

import (
	"fmt"
	"os"
	"os/exec"
)

const childEnvName = "MIRAGE_PROBE_CHILD"
const childEnvValue = "1"

func main() {
	if os.Getenv(childEnvName) == childEnvValue {
		fmt.Printf("child pid=%d ppid=%d\n", os.Getpid(), os.Getppid())
		return
	}

	fmt.Printf("parent pid=%d ppid=%d\n", os.Getpid(), os.Getppid())

	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), childEnvName+"="+childEnvValue)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "spawn-failed err=%v\n", err)
		os.Exit(1)
	}
}
