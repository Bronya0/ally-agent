//go:build server

package main

import (
	backend "ally-dev/internal/app"
	"os"
)

func main() { os.Exit(backend.RunHeadless(os.Args[1:])) }
