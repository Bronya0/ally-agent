// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"fmt"
	"os"

	"ally-dev/internal/tools/screenshot"
)

func runScreenshotCaptureHelper() bool {
	handled, err := screenshot.RunCaptureHelper(os.Args[1:])
	if handled && err != nil {
		fmt.Fprintln(os.Stderr, "Screenshot helper:", err)
		os.Exit(1)
	}
	return handled
}
