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

	NoticeBody = `swarmcli reports that it was started, so we know how many installs there are
and which releases are in use. It sends:

  • a random install id, generated on this machine just now
  • the version and edition
  • the OS, CPU architecture, and how swarmcli was installed

It does not send your cluster, node, service, stack or image names, your
hostnames, your command arguments, or your IP address. Your country is derived
from the connection and the address itself is never stored.

Turn it off with SWARMCLI_TELEMETRY=off — the update check still works.`
)

// ShouldNotice reports whether this run should show the disclosure.
//
// True exactly once per installation: on the run that has no stored identity
// yet and is about to create one. A run with telemetry already switched off
// never shows it, because there is nothing to disclose — and showing it anyway
// would be an advert rather than a notice.
func ShouldNotice() bool {
	return Enabled() && IsFirstRun()
}
