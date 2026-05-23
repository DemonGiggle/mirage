package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"time"
)

func main() {
	timeout := flag.Duration("timeout", 2*time.Second, "dial timeout")
	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: probe-tcp-connect [-timeout 2s] <host:port>")
		os.Exit(2)
	}

	addr := flag.Arg(0)
	conn, err := net.DialTimeout("tcp", addr, *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect-failed addr=%s err=%v\n", addr, err)
		os.Exit(1)
	}
	_ = conn.Close()

	fmt.Printf("connect-ok addr=%s\n", addr)
}
