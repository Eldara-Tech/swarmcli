// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package telemetry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// Env is the one variable that governs the startup request, and it has
	// three states rather than two — see State.
	//
	// Spelled as a value rather than as SWARMCLI_DISABLE_TELEMETRY so that
	// `SWARMCLI_TELEMETRY=off` reads the way people write it, and so the third
	// state had somewhere to go without a second variable.
	Env = "SWARMCLI_TELEMETRY"

	// legacyDisableEnv is the variable this replaced. Still honoured, no longer
	// documented.
	//
	// It is undocumented rather than deleted, and that is a deliberate
	// asymmetry. The people who set it are the ones running swarmcli where
	// outbound requests are a compliance question — air-gapped clusters, which
	// this product serves on purpose and which install leases from a file for
	// the same reason. Dropping support would mean an upgrade silently starts
	// making network calls on exactly those machines, and they would find out
	// from a firewall log rather than from us. A variable nobody is told about
	// but that keeps working costs a line here; the alternative costs somebody
	// an incident.
	legacyDisableEnv = "SWARMCLI_DISABLE_VERSION_CHECK"

	// DefaultURL is the telemetry endpoint, which also answers the update check.
	DefaultURL = "https://swarmcli.io/api/v1/telemetry"

	// FallbackURL is the original update check, and the whole of what is called
	// when telemetry is off: it carries no install id and nothing about the
	// machine.
	FallbackURL = "https://swarmcli.io/api/v1/version"

	requestTimeout = 4 * time.Second
)

// Events. A closed set matching the server's; an unknown name is refused there.
const (
	EventStarted   = "swarmcli_started"
	EventHeartbeat = "swarmcli_heartbeat"
)

// Modes. Which of the three products is asking.
const (
	ModeTUI = "tui"
	ModeCLI = "cli"
	ModeCD  = "cd"
)

// State is what the startup request does. Three states, because "no telemetry"
// and "no network" are different asks and conflating them served neither.
type State int

const (
	// StateFull reports usage and checks for updates, in one request. The
	// default.
	//
	// **On by default**, which is the decision worth being explicit about
	// rather than burying in a boolean. What makes that defensible is the shape
	// of what is sent: an install identifier and a country the server derives
	// and the client never sees, with no address stored and nothing about the
	// cluster. The first-run notice says so before the first report leaves.
	StateFull State = iota

	// StateUpdateOnly sends the version-only request: no install id and nothing
	// about the machine, byte-identical to what shipped before usage reporting
	// existed. The update notice still works, because being told a new release
	// exists is a feature somebody asked for by running swarmcli and is not
	// telemetry.
	StateUpdateOnly

	// StateSilent makes no outbound request at all. The state an air-gapped or
	// policy-restricted cluster wants, and the one no amount of "off" could
	// express while this was two variables.
	StateSilent
)

// Reporting reads the state from the environment.
//
// Anything unrecognised leaves reporting **on**. That is the deliberate
// direction for a value whose absence also means on: a typo in a chart cannot
// silently disable it, which would be a failure nobody notices until the
// numbers are already wrong.
func Reporting() State {
	set := strings.ToLower(strings.TrimSpace(os.Getenv(Env)))

	switch set {
	case "none", "silent":
		return StateSilent
	case "off", "false", "0", "no":
		return StateUpdateOnly
	}

	// The retired variable, honoured for the reason given at its declaration —
	// but only when the current one says nothing at all.
	//
	// The guard is `set == ""`, not "the switch above did not match". Somebody
	// who has written SWARMCLI_TELEMETRY has made a decision, and a stale
	// SWARMCLI_DISABLE_VERSION_CHECK left in the same chart must not quietly
	// overrule it — including when what they wrote is a spelling this does not
	// recognise, which lands on StateFull and is still their decision. Getting
	// this backwards silenced `SWARMCLI_TELEMETRY=on`, and a test caught it.
	if set == "" {
		if disabled, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(legacyDisableEnv))); err == nil && disabled {
			return StateSilent
		}
	}

	return StateFull
}

// Enabled reports whether a usage report will be sent.
func Enabled() bool { return Reporting() == StateFull }

// Shape is what kind of swarm this is, as counts.
//
// **Counts, never names.** `docs/license.md` publishes that line for the
// licence requests — "how many, never which ones" — and this holds to it: a
// node count is a fact about scale, a node name is a map of somebody's estate.
// Nothing here can be turned into the second.
//
// Pointers, so that absent and zero stay different facts. A swarm that has not
// been observed yet sends nothing; a swarm observed to be running no services
// sends `0`, and that is a real and different answer. Only `Services` can
// honestly be zero — a swarm always has at least the node you are asking — but
// all three are pointers so the next reader does not have to know which.
type Shape struct {
	Nodes         *int
	Managers      *int
	Services      *int
	DockerVersion string
}

// report is the telemetry request body. Mirrors the server's zod schema; a
// field it does not know is stripped there rather than rejected, so the two
// drifting apart fails quietly and the tests on both sides are what catch it.
type report struct {
	Event         string `json:"event"`
	InstallID     string `json:"install_id"`
	Version       string `json:"version"`
	Edition       string `json:"edition"`
	OS            string `json:"os,omitempty"`
	Arch          string `json:"arch,omitempty"`
	InstallMethod string `json:"install_method,omitempty"`
	Mode          string `json:"mode,omitempty"`

	Nodes         *int   `json:"nodes,omitempty"`
	Managers      *int   `json:"managers,omitempty"`
	Services      *int   `json:"services,omitempty"`
	DockerVersion string `json:"docker_version,omitempty"`
}

// versionOnly is the fallback body: exactly what has always been sent, so a
// client with telemetry off is indistinguishable from one that predates it.
type versionOnly struct {
	Version string `json:"version"`
	Edition string `json:"edition"`
}

type response struct {
	LatestVersion string `json:"latestVersion"`
}

// Client performs the one outbound call swarmcli makes on startup.
type Client struct {
	// URL and FallbackVersionURL are fields rather than constants so tests can
	// point them at a local server.
	URL                string
	FallbackVersionURL string
	HTTP               *http.Client
}

// New returns a client against the live endpoints.
func New() *Client {
	return &Client{
		URL:                DefaultURL,
		FallbackVersionURL: FallbackURL,
		HTTP:               &http.Client{Timeout: requestTimeout},
	}
}

// CheckIn reports one event and returns the newest released version.
//
// **The update check and the telemetry are one request, and that is the rule
// this whole change is built around.** swarmcli has always made exactly one
// call per launch, and adding usage reporting must not begin by doubling that:
// a second beacon per launch is both twice the traffic from every install and
// twice the surface to explain to somebody deciding whether to trust it. So the
// telemetry endpoint answers the version question too, and a launch stays one
// request whichever branch below is taken.
//
// The two branches are the opt-out. With telemetry on, the full report goes to
// the telemetry endpoint. With it off, the original version check runs
// unchanged — the update notice is a feature the user asked for by running
// swarmcli, not telemetry, and switching off usage reporting must not also stop
// telling them a new release exists.
func (c *Client) CheckIn(event, version, edition, mode string, shape Shape) (string, error) {
	if Enabled() {
		installID := InstallID()
		// No storable identity means no report: a per-process id would make
		// every launch a new install and inflate the one number this exists to
		// produce. Fall through to the plain version check instead.
		if installID != "" {
			return c.post(c.URL, report{
				Event:         event,
				InstallID:     installID,
				Version:       version,
				Edition:       edition,
				OS:            OS(),
				Arch:          Arch(),
				InstallMethod: InstallMethod(),
				Mode:          mode,
				Nodes:         shape.Nodes,
				Managers:      shape.Managers,
				Services:      shape.Services,
				DockerVersion: shape.DockerVersion,
			})
		}
	}

	return c.post(c.FallbackVersionURL, versionOnly{Version: version, Edition: edition})
}

func (c *Client) post(url string, payload any) (string, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return "", fmt.Errorf("telemetry URL is empty")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal telemetry payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build telemetry request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: requestTimeout}
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("send telemetry request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("telemetry API status: %s", resp.Status)
	}

	var decoded response
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return "", fmt.Errorf("decode telemetry response: %w", err)
	}

	return strings.TrimSpace(decoded.LatestVersion), nil
}
