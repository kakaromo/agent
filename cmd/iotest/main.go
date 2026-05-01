package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	filePath := flag.String("f", "", "path to JSON config file")
	flag.Parse()

	var input []byte
	var err error

	if *filePath != "" {
		input, err = os.ReadFile(*filePath)
	} else {
		input, err = io.ReadAll(os.Stdin)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, `{"error":"failed to read input: %s"}`+"\n", err)
		os.Exit(1)
	}

	var cfg Config
	if err := json.Unmarshal(input, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, `{"error":"invalid JSON: %s"}`+"\n", err)
		os.Exit(1)
	}

	if len(cfg.Threads) == 0 {
		fmt.Fprintf(os.Stderr, `{"error":"no threads defined"}`+"\n")
		os.Exit(1)
	}

	engine := NewEngine(cfg)
	result := engine.Run()

	out, _ := json.MarshalIndent(result, "", "  ")
	fmt.Fprintln(os.Stdout, string(out))

	// Exit with error code if any thread failed
	for _, tr := range result.Threads {
		if tr.Errors > 0 {
			os.Exit(1)
		}
	}
}
