// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package telemetry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// captured is a superset of both request bodies, so one struct can tell which
// of the two the client sent.
type captured struct {
	Event         string `json:"event"`
	InstallID     string `json:"install_id"`
	Version       string `json:"version"`
	Edition       string `json:"edition"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	InstallMethod string `json:"install_method"`
	Mode          string `json:"mode"`
}

// serve returns a client pointed at a recording server, with HOME redirected so
// the install identity lands in the test's own directory.
func serve(t *testing.T, body string) (*Client, *captured, *string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	var got captured
	var hitPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitPath = r.URL.Path
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	return &Client{URL: server.URL + "/telemetry", FallbackVersionURL: server.URL + "/version"}, &got, &hitPath
}

func TestCheckIn_ReportsTheFullEventByDefault(t *testing.T) {
	// Opt-out: with nothing set, the report goes out. That default is the whole
	// consent decision, so it is pinned rather than left to be inferred.
	c, got, path := serve(t, `{"latestVersion":"1.14.15"}`)

	latest, err := c.CheckIn(EventStarted, "1.2.2", "ce", ModeTUI)

	require.NoError(t, err)
	require.Equal(t, "1.14.15", latest)
	require.Equal(t, "/telemetry", *path)
	require.Equal(t, EventStarted, got.Event)
	require.NotEmpty(t, got.InstallID)
	require.Equal(t, "1.2.2", got.Version)
	require.Equal(t, "ce", got.Edition)
	require.Equal(t, ModeTUI, got.Mode)
	require.NotEmpty(t, got.OS)
	require.NotEmpty(t, got.Arch)
}

func TestCheckIn_OffFallsBackToThePlainVersionCheck(t *testing.T) {
	// The update notice is a feature somebody asked for by running swarmcli, not
	// telemetry. Switching reporting off must not also stop telling them a new
	// release exists — and the body must carry no identity.
	c, got, path := serve(t, `{"latestVersion":"1.14.15"}`)
	t.Setenv(DisableEnv, "off")

	latest, err := c.CheckIn(EventStarted, "1.2.2", "ce", ModeTUI)

	require.NoError(t, err)
	require.Equal(t, "1.14.15", latest)
	require.Equal(t, "/version", *path)
	require.Empty(t, got.InstallID)
	require.Empty(t, got.OS)
	require.Empty(t, got.Event)
	require.Equal(t, "1.2.2", got.Version)
	require.Equal(t, "ce", got.Edition)
}

func TestCheckIn_WritesNoIdentityWhenDisabled(t *testing.T) {
	// Off means off: the run must not leave an install id behind, or switching
	// telemetry on later would silently reuse an identity created while it was
	// off.
	c, _, _ := serve(t, `{"latestVersion":"1.14.15"}`)
	t.Setenv(DisableEnv, "off")

	_, err := c.CheckIn(EventStarted, "1.2.2", "ce", ModeTUI)
	require.NoError(t, err)

	require.True(t, IsFirstRun(), "a disabled run must not create an install identity")
}

func TestEnabled_AcceptsTheSpellingsPeopleActuallyUse(t *testing.T) {
	for _, off := range []string{"off", "OFF", "false", "False", "0", "no", " off "} {
		t.Run(off, func(t *testing.T) {
			t.Setenv(DisableEnv, off)
			require.False(t, Enabled())
		})
	}

	for _, on := range []string{"", "on", "true", "1", "yes", "banana"} {
		t.Run("on/"+on, func(t *testing.T) {
			t.Setenv(DisableEnv, on)
			require.True(t, Enabled(), "anything not recognised as off leaves it on")
		})
	}
}

func TestInstallID_IsStableAcrossCalls(t *testing.T) {
	// The entire value of the identifier is that it is the same tomorrow. A
	// regression here would not fail anything else — it would just quietly turn
	// every launch into a new install.
	t.Setenv("HOME", t.TempDir())

	first := InstallID()
	require.NotEmpty(t, first)
	require.Equal(t, first, InstallID())
	require.Equal(t, first, InstallID())
}

func TestInstallID_IsAUUIDAndNotDerivedFromTheMachine(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := InstallID()

	t.Setenv("HOME", t.TempDir())
	b := InstallID()

	require.Regexp(t, `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`, a)
	require.NotEqual(t, a, b, "two installations on one machine must not share an id")
}

func TestIsFirstRun_TrueOnlyBeforeAnIdentityExists(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	require.True(t, IsFirstRun())
	InstallID()
	require.False(t, IsFirstRun())
}

func TestShouldNotice_OnlyOnceAndOnlyWhenReporting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	require.True(t, ShouldNotice(), "the first reporting run discloses")

	InstallID()
	require.False(t, ShouldNotice(), "and never again")

	t.Setenv("HOME", t.TempDir())
	t.Setenv(DisableEnv, "off")
	require.False(t, ShouldNotice(), "a disabled run has nothing to disclose")
}

func TestNotice_EnumeratesWhatIsSentAndNamesTheOffSwitch(t *testing.T) {
	// The notice is what makes opt-out legitimate, so its content is a contract
	// rather than copy. If a field is added to the report and not to this list,
	// the notice becomes untrue.
	for _, want := range []string{"install id", "version", "OS", DisableEnv} {
		require.Contains(t, NoticeBody, want)
	}

	// And the reassurance somebody running this against production actually
	// wants, which is about what is NOT sent.
	for _, want := range []string{"cluster", "hostnames", "command arguments", "IP address"} {
		require.Contains(t, NoticeBody, want)
	}
}

func TestInstallMethod_IsConservative(t *testing.T) {
	restore := executableFn
	t.Cleanup(func() { executableFn = restore })
	restoreStat := statFn
	t.Cleanup(func() { statFn = restoreStat })
	// Keep the container probe out of it: on a machine that happens to be in a
	// container every case below would answer "docker".
	statFn = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }

	cases := map[string]string{
		"/opt/homebrew/Cellar/swarmcli/2.0.0/bin/swarmcli": MethodBrew,
		"/home/u/.linuxbrew/bin/swarmcli":                  MethodBrew,
		`C:\Users\u\scoop\shims\swarmcli.exe`:              MethodScoop,
		"/tmp/go-build1234/b001/exe/swarmcli":              MethodSource,
		"/usr/local/bin/swarmcli":                          MethodUnknown,
		"/home/u/bin/swarmcli":                             MethodUnknown,
	}

	for path, want := range cases {
		t.Run(path, func(t *testing.T) {
			executableFn = func() (string, error) { return path, nil }
			require.Equal(t, want, InstallMethod())
		})
	}
}

func TestInstallMethod_UnknownRatherThanAGuessWhenTheExecutableIsUnreadable(t *testing.T) {
	restore := executableFn
	t.Cleanup(func() { executableFn = restore })
	restoreStat := statFn
	t.Cleanup(func() { statFn = restoreStat })
	statFn = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	executableFn = func() (string, error) { return "", os.ErrNotExist }

	require.Equal(t, MethodUnknown, InstallMethod())
}
