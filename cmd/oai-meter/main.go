package main

import (
	"fmt"
	"os"

	"github.com/cornelia/oai-response-meter/internal/app"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "oai-meter: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(os.Stdout, "oai-meter captures OpenAI usage metadata through mitmproxy.")
		return nil
	}
	if args[0] == "run" {
		return app.RunCommand(args[1:])
	}
	return fmt.Errorf("unknown command %q", args[0])
}
