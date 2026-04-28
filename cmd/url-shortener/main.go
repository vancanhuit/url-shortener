// Command url-shortener is the entry point for the URL shortener service.
// The actual command tree lives in internal/cli.
package main

import (
	"os"

	"github.com/vancanhuit/url-shortener/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
