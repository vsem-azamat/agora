// Command agora coordinates AI coding agents on your machines.
package main

import (
	"context"
	"os"

	"github.com/vsem-azamat/agora/internal/cli"
)

func main() {
	os.Exit(cli.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
