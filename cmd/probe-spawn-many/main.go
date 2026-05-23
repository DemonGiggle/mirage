package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"time"
)

const childEnvName = "MIRAGE_PROBE_SPAWN_MANY_CHILD"

func main() {
	count := flag.Int("count", 3, "number of child processes to start")
	sleepDuration := flag.Duration("sleep", 500*time.Millisecond, "how long each child should stay alive")
	flag.Parse()

	if os.Getenv(childEnvName) == "1" {
		time.Sleep(*sleepDuration)
		return
	}
	if *count <= 0 {
		fmt.Fprintln(os.Stderr, "usage: probe-spawn-many -count <positive integer>")
		os.Exit(2)
	}

	var started []*exec.Cmd
	for i := 0; i < *count; i++ {
		cmd := exec.Command(os.Args[0], "-count", "1", "-sleep", sleepDuration.String())
		cmd.Env = append(os.Environ(), childEnvName+"=1")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			for _, child := range started {
				if child.Process != nil {
					_ = child.Process.Kill()
				}
			}
			for _, child := range started {
				_ = child.Wait()
			}
			fmt.Fprintf(os.Stderr, "spawn-failed index=%d err=%v\n", i, err)
			os.Exit(1)
		}
		started = append(started, cmd)
	}

	for _, child := range started {
		if err := child.Wait(); err != nil {
			fmt.Fprintf(os.Stderr, "child-failed err=%v\n", err)
			os.Exit(1)
		}
	}

	fmt.Printf("spawn-ok count=%d\n", *count)
}
