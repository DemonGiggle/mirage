package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: probe-http-get <url>")
		os.Exit(2)
	}

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "http-failed url=%s err=%v\n", os.Args[1], err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "http-read-failed url=%s err=%v\n", os.Args[1], err)
		os.Exit(1)
	}

	fmt.Printf("http-ok url=%s status=%d bytes=%d\n", os.Args[1], resp.StatusCode, len(body))
}
