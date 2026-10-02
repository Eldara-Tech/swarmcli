// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// A Release is stored as YAML in a Docker Config and served as JSON. The two
// must name the same fields the same way, or the API and the stored record
// describe the same release in two vocabularies.
func TestReleaseJSONMirrorsYAMLKeys(t *testing.T) {
	rel := Release{
		Name:      "edge",
		Revision:  3,
		Status:    StatusDeployed,
		Chart:     ReleaseChart{Name: "traefik", Version: "0.1.1", AppVersion: "3.1"},
		Values:    map[string]any{"replicas": 2},
		Manifest:  "services: {}\n",
		Created:   "2026-07-21T00:00:00Z",
		Namespace: "edge",
	}

	var got map[string]any
	b, err := json.Marshal(rel)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &got))

	// "release", not "name" — mirroring the yaml tag rather than the Go field.
	require.Contains(t, got, "release")
	require.NotContains(t, got, "Name")
	for _, k := range []string{"revision", "status", "chart", "values", "manifest", "created", "namespace"} {
		require.Contains(t, got, k, "missing key %q", k)
	}
	// Absent and omitempty: must not appear.
	require.NotContains(t, got, "managedNetworks")

	chart, ok := got["chart"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "traefik", chart["name"])
	require.Equal(t, "3.1", chart["appVersion"])
	// No source recorded: omitempty keeps both keys out.
	require.NotContains(t, chart, "repo")
	require.NotContains(t, chart, "repoURL")

	b, err = json.Marshal(ReleaseChart{Name: "traefik", Repo: "swarmcli-charts", RepoURL: "https://eldara-tech.github.io/swarmcli-charts"})
	require.NoError(t, err)
	var sourced map[string]any
	require.NoError(t, json.Unmarshal(b, &sourced))
	require.Equal(t, "swarmcli-charts", sourced["repo"])
	require.Equal(t, "https://eldara-tech.github.io/swarmcli-charts", sourced["repoURL"])
}

func TestPlanJSONKeys(t *testing.T) {
	p := Plan{
		Releases: []ReleasePlan{{
			Name:      "edge",
			Ref:       "swarmcli-charts/traefik",
			Action:    ActionUpgrade,
			ToVersion: "0.1.1",
			Manifest:  "services: {}\n",
		}},
	}

	var got map[string]any
	b, err := json.Marshal(p)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &got))

	require.Contains(t, got, "releases")
	// Unmanaged is empty here and omitempty, so it must be absent rather than null.
	require.NotContains(t, got, "unmanaged")

	rels, ok := got["releases"].([]any)
	require.True(t, ok)
	rp, ok := rels[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "edge", rp["name"])
	require.Equal(t, "upgrade", rp["action"])
	require.Equal(t, "0.1.1", rp["toVersion"])
	// Empty for an install/unchanged plan; omitempty keeps it out.
	require.NotContains(t, rp, "fromVersion")
	require.NotContains(t, rp, "currentManifest")
	// Wave 0 is both the default and "explicitly first" — the same thing, since
	// there is no unset wave to tell apart — so omitting it loses nothing and
	// keeps a plan from a file that declares no wave byte-identical to what it
	// has always served.
	require.NotContains(t, rp, "wave")
}

// A declared wave reaches the wire, because a consumer rendering a plan has to
// be able to show the order it will be applied in.
func TestPlanJSONCarriesADeclaredWave(t *testing.T) {
	b, err := json.Marshal(Plan{Releases: []ReleasePlan{{Name: "api", Wave: 2}}})
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got))
	rp := got["releases"].([]any)[0].(map[string]any)
	require.Equal(t, float64(2), rp["wave"])
}

func TestApplyResultJSONKeys(t *testing.T) {
	var got map[string]any
	b, err := json.Marshal(ApplyResult{Name: "edge", Action: ActionUnchanged})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &got))

	require.Equal(t, "edge", got["name"])
	require.Equal(t, "unchanged", got["action"])
	// Revision is 0 when unchanged (nothing recorded) and omitempty drops it,
	// so a consumer cannot mistake it for revision zero.
	require.NotContains(t, got, "revision")
}

// The owner stamp is served to API clients under the same key it is stored
// under, and stays absent — not null, not "" — when nothing claimed the release.
func TestReleaseOwnerJSONKey(t *testing.T) {
	var got map[string]any
	b, err := json.Marshal(Release{Name: "edge", Owner: "apply/prod:release/edge"})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &got))
	require.Equal(t, "apply/prod:release/edge", got["owner"])

	var bare map[string]any
	b, err = json.Marshal(Release{Name: "edge"})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &bare))
	require.NotContains(t, bare, "owner")
}

// A plan distinguishes releases it provably installed from releases of unknown
// origin, and both keys stay absent when empty.
func TestPlanOwnerAndOrphanedJSONKeys(t *testing.T) {
	var got map[string]any
	b, err := json.Marshal(Plan{Owner: "apply/prod", Orphaned: []string{"gone"}})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &got))
	require.Equal(t, "apply/prod", got["owner"])
	require.Equal(t, []any{"gone"}, got["orphaned"])

	var bare map[string]any
	b, err = json.Marshal(Plan{})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &bare))
	require.NotContains(t, bare, "owner")
	require.NotContains(t, bare, "orphaned")
}

// A compatibility finding is served alongside the plan, so it has to speak the
// same JSON as everything around it: lowercase keys, and a status a client can
// read without a copy of this package's constant block.
func TestCompatFindingJSONShape(t *testing.T) {
	var got map[string]any
	b, err := json.Marshal(CompatFinding{
		Chart: "traefik 0.1.1", Required: ">= 1.13.0", Engine: "1.13.0", Status: CompatOK,
	})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &got))
	require.Equal(t, map[string]any{
		"chart": "traefik 0.1.1", "required": ">= 1.13.0", "engine": "1.13.0", "status": "ok",
	}, got)
}

// The zero value must read as "unknown", not as a claim about the chart: an
// unset status is the one callers are explicitly told not to block on.
func TestCompatStatusNames(t *testing.T) {
	require.Equal(t, "unknown", CompatUnknown.String())
	require.Equal(t, "ok", CompatOK.String())
	require.Equal(t, "incompatible", CompatIncompatible.String())
}

// Marshalling to a name would be a regression if it could not be read back, so
// the type still round-trips. An unrecognised name from a newer producer
// degrades to unknown rather than failing the whole document.
func TestCompatStatusRoundTrips(t *testing.T) {
	for _, s := range []CompatStatus{CompatUnknown, CompatOK, CompatIncompatible} {
		b, err := json.Marshal(s)
		require.NoError(t, err)
		var back CompatStatus
		require.NoError(t, json.Unmarshal(b, &back))
		require.Equal(t, s, back)
	}

	var back CompatStatus
	require.NoError(t, json.Unmarshal([]byte(`"from-the-future"`), &back))
	require.Equal(t, CompatUnknown, back)
}
