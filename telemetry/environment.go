// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package telemetry

import (
	"os"
	"runtime"
	"strings"
)

// Overridable for tests.
var (
	executableFn = os.Executable
	statFn       = os.Stat
)

// Install methods. A closed set, matching the server's zod enum: a value
// outside it is refused there, so inventing one here would silently drop the
// whole event rather than just the field.
const (
	MethodBrew    = "brew"
	MethodScoop   = "scoop"
	MethodDocker  = "docker"
	MethodSource  = "source"
	MethodTarball = "tarball"
	MethodUnknown = "unknown"
)

// OS and Arch are the build's, from the runtime rather than from the machine.
// `runtime.GOOS` is what swarmcli was compiled for, which is the thing worth
// knowing when deciding which builds to keep publishing.
func OS() string   { return runtime.GOOS }
func Arch() string { return runtime.GOARCH }

// InstallMethod guesses how this binary got here, and says "unknown" when it
// cannot tell.
//
// The question it answers is which distribution channel is worth maintaining —
// the Homebrew tap, the Scoop bucket, the Docker image or the release tarball.
// Nothing in swarmcli has ever recorded it, so today that is decided on
// nothing.
//
// **It is a guess from the executable's path, and deliberately conservative.**
// Everything it cannot place is `unknown` rather than `tarball`, even though a
// tarball is the likeliest answer for an unmatched path: a default that claims
// the most common channel would make that channel look better than it is, and
// the number would then be used to justify keeping it. An honest `unknown`
// bucket is something a reader can see and discount; a wrong `tarball` is not.
func InstallMethod() string {
	// Checked before the path, because a container's binary can sit anywhere
	// and the container is the more specific fact about how it is run.
	if inDocker() {
		return MethodDocker
	}

	exe, err := executableFn()
	if err != nil {
		return MethodUnknown
	}

	lower := strings.ToLower(filepathToSlash(exe))

	switch {
	// Homebrew installs into the Cellar and links into bin. The prefixes differ
	// per platform: /usr/local on Intel macOS, /opt/homebrew on Apple Silicon,
	// and Linuxbrew under either /home/linuxbrew/.linuxbrew or ~/.linuxbrew.
	//
	// Both spellings of the Linuxbrew directory are matched because the common
	// one is dot-prefixed — `/linuxbrew/` alone misses `~/.linuxbrew/bin`, which
	// is where `brew install` puts it for a non-root user, and a test caught
	// exactly that.
	case strings.Contains(lower, "/cellar/"),
		strings.Contains(lower, "/homebrew/"),
		strings.Contains(lower, "/linuxbrew/"),
		strings.Contains(lower, "/.linuxbrew/"):
		return MethodBrew

	// Scoop shims live under ~/scoop/apps and ~/scoop/shims. Compared against
	// the slash-normalised path so the Windows separator does not matter.
	case strings.Contains(lower, "/scoop/"):
		return MethodScoop

	// `go run` builds into a temporary directory whose name carries this
	// marker; `go build` output in a work tree does not, and is indistinguishable
	// from a downloaded binary, so it stays unknown rather than being claimed.
	case strings.Contains(lower, "/go-build"):
		return MethodSource

	default:
		return MethodUnknown
	}
}

// filepathToSlash normalises separators without importing path/filepath purely
// for one call — and, more usefully, normalises a Windows path the same way on
// every platform, so the Scoop case is testable on Linux and macOS.
func filepathToSlash(p string) string {
	return strings.ReplaceAll(p, `\`, "/")
}

// inDocker reports whether this process is in a container.
//
// `/.dockerenv` is what Docker itself creates and is the cheapest reliable
// signal. The cgroup read is the fallback for runtimes that do not, and for
// Docker versions that stopped writing the file. Neither is authoritative —
// there is no syscall for "am I in a container" — and being wrong here costs a
// mislabelled row rather than anything that matters.
func inDocker() bool {
	if _, err := statFn("/.dockerenv"); err == nil {
		return true
	}
	data, err := readFileFn("/proc/self/cgroup")
	if err != nil {
		return false
	}
	content := string(data)
	return strings.Contains(content, "docker") || strings.Contains(content, "containerd")
}
