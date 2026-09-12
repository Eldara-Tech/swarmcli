// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package telemetry

// NoticeTitle and NoticeBody are the first-run disclosure.
//
// **This is the half of opt-out that makes it legitimate.** A default-on
// reporter with no notice is the thing people are right to object to; the
// difference between that and this is that the first run says what is being
// sent, before anything has been sent, and says how to stop it.
//
// Written to be read in three seconds, so it enumerates rather than explains.
// The enumeration is the point: it is the same list as `docs/license.md` and
// the same list the server's schema accepts, and if the three ever disagree the
// documentation is wrong rather than the reader.
//
// It names what is *not* sent as well, because that is the question somebody
// running this against a production swarm actually has, and answering it in the
// notice is cheaper than having them find the documentation to be reassured.
const (
	NoticeTitle = "Usage reporting is on"

	NoticeBody = `swarmcli reports that it was started, so we know how many installs there
are, which releases are in use, and what kind of swarms people run. It sends:

  • a random install id, generated on this machine just now
  • the version and edition
  • the OS, CPU architecture, and how swarmcli was installed
  • how many nodes, managers and services your swarm has, and its Docker
    version — counts only

It does not send your cluster, node, service, stack or image names, your
hostnames, your command arguments, or your IP address. Your country is derived
from the connection and the address itself is never stored.

Turn it off with SWARMCLI_TELEMETRY=off — the update check still works.
SWARMCLI_TELEMETRY=none sends nothing at all.`
)

// NoticeLine and NoticeLineShort are the same disclosure as one line, for the
// stack bar.
//
// **The full text moved to `:telemetry`; this is what the first run shows.** A
// modal that must be dismissed reads as a demand rather than a disclosure, and
// it arrives at the one moment somebody is trying to look at their swarm. The
// line says the two things that cannot wait — that reporting is on, and that it
// is counts rather than names — and names the command that prints the rest.
//
// Two lengths because the bar is shared. The short form is what survives on a
// narrow terminal, and it keeps the half a reader would miss the notice for:
// that reporting is happening, and where to read about it.
const (
	NoticeLine      = "Usage reporting is on · counts only, never names · :telemetry"
	NoticeLineShort = "Usage reporting on · :telemetry"
)

// StatusText is what `:telemetry` prints: the state this process is actually
// in, then the same enumeration the first run advertises.
//
// The state comes first because it is the question being asked. Somebody typing
// `:telemetry` after setting the variable wants to know whether it took effect,
// and an unconditional "usage reporting is on" above the enumeration would be a
// lie in two of the three states.
func StatusText() string {
	var state string
	switch Reporting() {
	case StateUpdateOnly:
		state = "Usage reporting is OFF (" + Env + "=off). The update check still runs."
	case StateSilent:
		state = "Usage reporting is OFF and no request is made at all (" + Env + "=none)."
	default:
		state = NoticeTitle + "."
	}
	return state + "\n\n" + NoticeBody
}

// ShouldNotice reports whether this run should show the disclosure.
//
// True exactly once per installation: on the run that has no stored identity
// yet and is about to create one. A run with telemetry already switched off
// never shows it, because there is nothing to disclose — and showing it anyway
// would be an advert rather than a notice.
func ShouldNotice() bool {
	return Enabled() && IsFirstRun()
}
