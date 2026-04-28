// Command url-shortener is the entry point for the URL shortener service.
//
// Subcommands such as `run`, `migrate`, `version`, and `config` are provided
// by the github.com/vancanhuit/url-shortener/internal/cli package, which is
// implemented in a later phase. For now this binary only prints a placeholder
// message so that the project is buildable from the very first commit.
package main

import "fmt"

func main() {
	fmt.Println("url-shortener: scaffolding only; subcommands will be added in a later phase")
}
