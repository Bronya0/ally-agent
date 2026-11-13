// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"fmt"
	"os"

	"ally-dev/desktop"
	"ally-dev/internal/app"
)

func main() {
	args, headless, err := app.SplitHeadlessArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if headless {
		os.Exit(app.RunHeadless(args))
	}
	desktop.Run()
}
