// Command celeris is the Celeris inference API command-line interface.
package main

import (
	"os"

	"github.com/ai-celeris/celeris-cli/internal/cli"
)

func main() {
	os.Exit(cli.Main())
}
