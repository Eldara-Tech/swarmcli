// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package telemetry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// DisableEnv switches telemetry off. Any of off/false/0/no, case-insensitive.
	//
	// Spelled as a value rather than as SWARMCLI_DISABLE_TELEMETRY so that
	// `SWARMCLI_TELEMETRY=off` reads the way people write it, and so a future
	// third state has somewhere to go without a second variable.
	DisableEnv = "SWARMCLI_TELEMETRY"

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

// Enabled reports whether usage reporting is on.
//
// **On by default**, which is the decision worth being explicit about rather
// than burying in a boolean. What makes that defensible is the shape of what is
// sent: an install identifier and a country the server derives and the client
// never sees, with no address stored and nothing about the cluster. The
// first-run notice says so before the first report leaves, and this variable
// switches it off for good.
func Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(DisableEnv))) {
	case "off", "false", "0", "no":
		return false
	default:
		return true
	}
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
func (c *Client) CheckIn(event, version, edition, mode string) (string, error) {
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
