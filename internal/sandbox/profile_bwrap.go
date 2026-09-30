// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

package sandbox

// bwrapArgs renders the bubblewrap argv prefix (everything before the command).
//
// Mount order is the policy: bubblewrap applies mounts in argument order and the
// later mount wins, so the read-only host tree comes first, the writable binds
// next, and the re-deny / mask mounts last. The result mirrors the macOS
// profile: read almost everywhere, write only under the writable roots, and an
// optional network namespace that removes egress entirely.
//
// The host temp directory is deliberately left visible (no `--tmpfs /tmp`): a
// workspace that lives under /tmp would otherwise be shadowed. `--dev /dev` and
// `--proc /proc` mount fresh filesystems so the sandbox does not depend on the
// host's /dev and /proc layout, and `--die-with-parent` kills the sandboxed
// process when the runner is killed — without it, cancelling a run would leave
// the command running.
func bwrapArgs(writable []string, denyWrite, masks []policyPath, network bool) []string {
	args := []string{
		"--die-with-parent",
		// A pid namespace keeps the command from listing, signalling or reading
		// the environment of the host's other processes, and --proc below then
		// shows only the sandbox's own.
		"--unshare-pid",
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
	}
	if !network {
		args = append(args, "--unshare-net")
	}
	for _, dir := range writable {
		args = append(args, "--bind", dir, dir)
	}
	for _, entry := range denyWrite {
		// --ro-bind takes a single file as readily as a tree, so re-denying one
		// file needs no special case here.
		args = append(args, "--ro-bind", entry.path, entry.path)
	}
	for _, mask := range masks {
		if mask.dir {
			// A directory becomes an empty tmpfs and is then remounted read-only:
			// masking reads alone would leave a writable hole where writes
			// succeed and vanish with the sandbox, leaving a zero exit code and
			// no trace of what the command wrote.
			args = append(args, "--tmpfs", mask.path, "--remount-ro", mask.path)
			continue
		}
		// tmpfs cannot be mounted over a file; a read-only /dev/null reads as
		// empty and refuses writes.
		args = append(args, "--ro-bind", "/dev/null", mask.path)
	}
	return args
}
