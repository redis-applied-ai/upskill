package main

import (
	"os"

	"upskill/internal/cli"
)

var osExit = os.Exit

func main() {
	osExit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
