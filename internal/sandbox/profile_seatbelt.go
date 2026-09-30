// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

package sandbox

import (
	"fmt"
	"strings"
)

// seatbeltProfile renders the SBPL profile handed to `sandbox-exec -p`.
//
// The shape is "allow everything, deny every write, re-allow writes under the
// writable roots". Seatbelt evaluates rules in order and the LAST matching rule
// wins, which is exactly what makes the deny-then-allow sandwich work; the
// re-deny-write and deny-read rules are therefore emitted after their allow
// rules. Reads stay open everywhere else on purpose — a compiler reads GOROOT
// and git reads ~/.gitconfig, so a deny-by-default profile would break every
// toolchain. The line this draws is "cannot write outside the writable roots,
// and optionally cannot talk to the network".
func seatbeltProfile(writable []string, denyWrite, masks []policyPath, network bool) string {
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n(deny file-write*)\n(allow file-write*\n")
	for _, dir := range writable {
		fmt.Fprintf(&b, "    (subpath %s)\n", sbplQuote(dir))
	}
	b.WriteString(")\n")
	// Deny-write exceptions (a readable but read-only subtree inside the
	// workspace) must come after the allow block above. A single file takes a
	// literal rule: subpath would also match every path below it.
	for _, entry := range denyWrite {
		fmt.Fprintf(&b, "(deny file-write* (%s %s))\n", sbplMatch(entry), sbplQuote(entry.path))
	}
	for _, mask := range masks {
		fmt.Fprintf(&b, "(deny file-read* (subpath %s))\n", sbplQuote(mask.path))
	}
	if !network {
		b.WriteString("(deny network*)\n")
	}
	return b.String()
}

// sbplMatch picks the SBPL operator a policy entry needs.
func sbplMatch(entry policyPath) string {
	if entry.dir {
		return "subpath"
	}
	return "literal"
}

// sbplQuote renders a path as an SBPL string literal. Backslash and double quote
// are escaped so a path can never break out of the profile syntax.
func sbplQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
