// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package charts

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// fakeBackend is an in-memory Backend for lifecycle tests.
type fakeBackend struct {
	configs  map[string]fakeConfig
	deployed map[string]string // stack name -> manifest
	// lastDeploy is the whole request the last deploy carried. deployed keeps
	// only two of its fields, and Name, Manifest and Resolve are all strings,
	// so a pair of them swapped at the call site still fills that map.
	lastDeploy DeployRequest
	// deployOrder is the sequence deploys arrived in. deployed is a map, so
	// until this existed the order Apply converges releases in was not
	// observable at all — the closest any test could get was inferring it from
	// the first-failure semantics. Sync waves are entirely about that order, so
	// they are asserted on what reached the backend rather than on what Apply
	// reports having done.
	deployOrder []string
	// stackServiceCalls counts the reads a convergence wait makes, so a test can
	// state that a wave triggered no wait at all rather than infer it from
	// elapsed time.
	stackServiceCalls int
	volumes           map[string][]string
	services          map[string][]ServiceState
	networkScopes     map[string]string     // network name -> scope
	secrets           map[string]struct{}   // existing secret names
	createNetErr      map[string]error      // network name -> error to return on create
	createdNets       map[string]createdNet // network name -> driver/attachable used on create
	failNext          bool
	rmStackErr        error
	refreshErr        error
	secretsErr        error                   // error to return from SecretNames
	onCreate          func(name string) error // hook to simulate concurrent config creation
	deleteCfgErr      map[string]error        // config name -> error to return on delete
	// listData makes ListConfigs carry each payload, as the Docker backend
	// does. Off by default so the rest of the suite keeps exercising the
	// inspect fallback a Backend that omits it relies on.
	listData bool
	inspects int // InspectConfig calls, to prove the payload came from the list
}

type fakeConfig struct {
	data   []byte
	labels map[string]string
}

type createdNet struct {
	driver     string
	attachable bool
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		configs:       map[string]fakeConfig{},
		deployed:      map[string]string{},
		volumes:       map[string][]string{},
		services:      map[string][]ServiceState{},
		networkScopes: map[string]string{},
		secrets:       map[string]struct{}{},
		createNetErr:  map[string]error{},
	}
}

func (f *fakeBackend) DeployStack(_ context.Context, req DeployRequest) error {
	if f.failNext {
		f.failNext = false
		return fmt.Errorf("boom")
	}
	f.deployed[req.Name] = req.Manifest
	f.deployOrder = append(f.deployOrder, req.Name)
	f.lastDeploy = req
	return nil
}
func (f *fakeBackend) RemoveStack(_ context.Context, name string) error {
	if f.rmStackErr != nil {
		return f.rmStackErr
	}
	delete(f.deployed, name)
	return nil
}
func (f *fakeBackend) RefreshSnapshot(context.Context) error { return f.refreshErr }
func (f *fakeBackend) CreateConfig(_ context.Context, name string, data []byte, labels map[string]string) error {
	if f.onCreate != nil {
		if err := f.onCreate(name); err != nil {
			return err
		}
	}
	if _, ok := f.configs[name]; ok {
		return fmt.Errorf("config %q already exists", name)
	}
	f.configs[name] = fakeConfig{data: data, labels: labels}
	return nil
}
func (f *fakeBackend) ListConfigs(context.Context) ([]ConfigMeta, error) {
	var out []ConfigMeta
	for name, c := range f.configs {
		m := ConfigMeta{Name: name, Labels: c.labels}
		if f.listData {
			m.Data = c.data
		}
		out = append(out, m)
	}
	return out, nil
}
func (f *fakeBackend) InspectConfig(_ context.Context, name string) ([]byte, error) {
	f.inspects++
	c, ok := f.configs[name]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return c.data, nil
}
func (f *fakeBackend) DeleteConfig(_ context.Context, name string) error {
	if err := f.deleteCfgErr[name]; err != nil {
		return err
	}
	delete(f.configs, name)
	return nil
}
func (f *fakeBackend) StackServices(_ context.Context, name string) []ServiceState {
	f.stackServiceCalls++
	return f.services[name]
}
func (f *fakeBackend) StackVolumes(_ context.Context, name string) ([]string, error) {
	return f.volumes[name], nil
}
func (f *fakeBackend) RemoveVolume(_ context.Context, name string) error {
	for stack, vols := range f.volumes {
		out := vols[:0]
		for _, v := range vols {
			if v != name {
				out = append(out, v)
			}
		}
		f.volumes[stack] = out
	}
	return nil
}

func (f *fakeBackend) NetworkScopes(context.Context) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range f.networkScopes {
		out[k] = v
	}
	return out, nil
}
func (f *fakeBackend) CreateOverlayNetwork(_ context.Context, name, driver string, attachable bool) error {
	if err := f.createNetErr[name]; err != nil {
		return err
	}
	if f.createdNets == nil {
		f.createdNets = map[string]createdNet{}
	}
	f.createdNets[name] = createdNet{driver: driver, attachable: attachable}
	f.networkScopes[name] = "swarm"
	return nil
}
func (f *fakeBackend) RemoveOverlayNetwork(_ context.Context, name string) error {
	delete(f.networkScopes, name)
	return nil
}
func (f *fakeBackend) SecretNames(context.Context) (map[string]struct{}, error) {
	if f.secretsErr != nil {
		return nil, f.secretsErr
	}
	out := make(map[string]struct{}, len(f.secrets))
	for k := range f.secrets {
		out[k] = struct{}{}
	}
	return out, nil
}

func testEngine(b Backend) *Engine {
	e := NewEngineWith(b)
	e.now = func() time.Time { return time.Unix(1700000000, 0).UTC() }
	return e
}

func TestInstallRecordsRevisionOne(t *testing.T) {
	fb := newFakeBackend()
	e := testEngine(fb)
	ctx := context.Background()

	rel, err := e.Install(ctx, "my-demo", ReleaseChart{Name: "demo", Version: "0.1.0"},
		map[string]any{"replicas": 1}, "version: \"3.9\"\nservices:\n  app:\n    image: x\n", InstallOptions{})
	require.NoError(t, err)
	require.Equal(t, 1, rel.Revision)
	require.Equal(t, StatusDeployed, rel.Status)
	require.Equal(t, rel.Manifest, fb.deployed["my-demo"])

	cfg := fb.configs[releaseConfigName("my-demo", 1)]
	require.Equal(t, TypeRelease, cfg.labels[LabelType])
	require.Equal(t, "my-demo", cfg.labels[LabelRelease])
	require.Equal(t, "1", cfg.labels[LabelRevision])
}

// The three strings deployAndRecord unpacks into a DeployRequest come from
// three different places — the release's name, the release's manifest and the
// caller's options — and all three fields are strings, so a pair of them
// swapped at the call site compiles. Resolve is the one nothing else would
// notice: no other test reads it back, so a deploy resolving images the wrong
// way stays green. This reads all three apart.
func TestDeployRequestCarriesEachFieldToItsOwnPlace(t *testing.T) {
	fb := newFakeBackend()
	e := testEngine(fb)
	const manifest = "version: \"3.9\"\nservices:\n  app:\n    image: x\n"

	_, err := e.Install(context.Background(), "my-demo", ReleaseChart{Name: "demo", Version: "0.1.0"},
		nil, manifest, InstallOptions{ResolveImage: "changed"})
	require.NoError(t, err)

	require.Equal(t, "my-demo", fb.lastDeploy.Name, "Name must be the release name")
	require.Equal(t, manifest, fb.lastDeploy.Manifest, "Manifest must be the rendered manifest")
	require.Equal(t, "changed", fb.lastDeploy.Resolve, "Resolve must be InstallOptions.ResolveImage")
}

func TestInstallRejectsExisting(t *testing.T) {
	fb := newFakeBackend()
	e := testEngine(fb)
	ctx := context.Background()
	_, err := e.Install(ctx, "demo", ReleaseChart{Name: "demo", Version: "1"}, nil, "services:\n  a:\n    image: x\n", InstallOptions{})
	require.NoError(t, err)
	_, err = e.Install(ctx, "demo", ReleaseChart{Name: "demo", Version: "1"}, nil, "services:\n  a:\n    image: x\n", InstallOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "already exists")
}

func TestInstallDryRunDoesNotDeploy(t *testing.T) {
	fb := newFakeBackend()
	e := testEngine(fb)
	rel, err := e.Install(context.Background(), "demo", ReleaseChart{Name: "demo", Version: "1"}, nil, "services:\n  a:\n    image: x\n", InstallOptions{DryRun: true})
	require.NoError(t, err)
	require.Equal(t, 1, rel.Revision)
	require.Empty(t, fb.deployed)
	require.Empty(t, fb.configs)
}

// A failed deploy must record nothing, so the install stays retryable rather
// than leaving an orphaned "already exists" release Config behind.
func TestInstallFailureDoesNotRecord(t *testing.T) {
	fb := newFakeBackend()
	fb.failNext = true
	e := testEngine(fb)
	manifest := "services:\n  a:\n    image: x\n"

	rel, err := e.Install(context.Background(), "demo", ReleaseChart{Name: "demo", Version: "1"}, nil, manifest, InstallOptions{})
	require.Error(t, err)
	require.Equal(t, StatusFailed, rel.Status)
	require.Empty(t, fb.configs, "failed deploy must not persist a release Config")

	// failNext cleared itself; a retry now succeeds at revision 1.
	rel, err = e.Install(context.Background(), "demo", ReleaseChart{Name: "demo", Version: "1"}, nil, manifest, InstallOptions{})
	require.NoError(t, err)
	require.Equal(t, 1, rel.Revision)
	require.Equal(t, StatusDeployed, rel.Status)
}

// incompressible returns n bytes gzip cannot shrink, so a test can exceed a
// limit measured after compression without depending on how well anything
// compresses.
func incompressible(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return b
}

// The record's size ceiling is checked before the deploy, not after it.
//
// It used to be reached only from record(), which runs once the stack is
// already up — so an over-limit release deployed and then vanished: no Config
// means no list, no history, no status and no rollback, and the engine that
// made it cannot even uninstall it. Nothing had hit it while the budget was a
// ~500 KiB gzipped manifest; a chart's files share that budget and a couple of
// certificates reach it, which is what turns a latent defect into a live one.
func TestAnOversizedRecordRefusesBeforeAnythingIsDeployed(t *testing.T) {
	fb := newFakeBackend()
	e := testEngine(fb)
	// fb.deployed is the recorder: DeployStack is the only thing that writes it.
	//
	// More raw bytes than the limit, and incompressible, so the record is over
	// budget however it happens to encode them — the assertion is about the
	// check running early, not about a particular serialisation surviving.
	files := map[string][]byte{"files/tls/ca.pem": incompressible(t, 520<<10)}

	rel, err := e.Install(context.Background(), "demo", ReleaseChart{Name: "demo", Version: "1"},
		nil, "services:\n  a:\n    image: x\n", InstallOptions{Files: files})
	require.Error(t, err)
	require.Empty(t, fb.deployed, "the stack must not be deployed when its record cannot be written")
	require.Empty(t, fb.configs)
	require.Equal(t, StatusFailed, rel.Status)

	// The sizes, and the file that spends them: gzipped total alone names
	// nothing the operator can shrink.
	require.Contains(t, err.Error(), "Docker Config limit")
	require.Contains(t, err.Error(), strconv.Itoa(maxConfigPayload))
	require.Contains(t, err.Error(), "files/tls/ca.pem: 532480 bytes")
	require.Contains(t, err.Error(), "external: true")
}

// The same release without the files fits, so the refusal above is about size
// and not about carrying files at all.
func TestARecordedRevisionCarriesItsFiles(t *testing.T) {
	fb := newFakeBackend()
	e := testEngine(fb)
	files := map[string][]byte{"files/nginx.conf": []byte("server { listen 80; }")}

	_, err := e.Install(context.Background(), "demo", ReleaseChart{Name: "demo", Version: "1"},
		nil, "services:\n  a:\n    image: x\n", InstallOptions{Files: files})
	require.NoError(t, err)
	require.Equal(t, files, fb.lastDeploy.Files)

	stored, err := decodeRelease(fb.configs[releaseConfigName("demo", 1)].data)
	require.NoError(t, err)
	// The conversion is require.Equal's, not the code's: Release.Files is the
	// named releaseFiles so YAML stores base64, and reflect.DeepEqual compares
	// types before contents.
	require.Equal(t, files, map[string][]byte(stored.Files))
}

// storedYAML is the record as storeRevision wrote it, ungzipped: the exact
// bytes the Docker Config holds, which is the only place the stored format is
// visible.
func storedYAML(t *testing.T, fb *fakeBackend, release string, rev int) string {
	t.Helper()
	r, err := gzip.NewReader(bytes.NewReader(fb.configs[releaseConfigName(release, rev)].data))
	require.NoError(t, err)
	raw, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	return string(raw)
}

// The stored form of Release.Files is base64, not yaml.v3's default for a
// []byte — a sequence node holding one decimal integer per byte.
//
// The default is not merely ugly. It costs ~13 characters of YAML per byte of
// content and, measured through this path, 18,365 gzipped bytes for 100 KiB of
// config text where base64 costs 5,784; the record shares one ~500 KiB Docker
// Config budget with the manifest, so the difference is chart content that
// could not ship. See releaseFiles for the numbers and for why a stored format
// is not free to change once releases exist in the field.
func TestARecordedRevisionStoresItsFilesAsBase64(t *testing.T) {
	fb := newFakeBackend()
	e := testEngine(fb)
	text := []byte("server { listen 80; }")
	// Bytes no text encoding carries, so nothing can quietly degrade to a plain
	// YAML string and still round-trip.
	binary := []byte{0x00, 0xff, 0xfe, 0x01, 0x80, 0x0a, 0x7f}

	rel := &Release{
		Name: "demo", Revision: 1, Status: StatusDeployed,
		Chart:    ReleaseChart{Name: "demo", Version: "1"},
		Manifest: "services:\n  a:\n    image: x\n",
		Files:    releaseFiles{"files/nginx.conf": text, "files/tls/ca.der": binary},
	}
	require.NoError(t, e.storeRevision(context.Background(), rel))

	stored := storedYAML(t, fb, "demo", 1)
	require.Contains(t, stored, base64.StdEncoding.EncodeToString(text))
	require.Contains(t, stored, base64.StdEncoding.EncodeToString(binary))
	// 115 is 's', the first element of the sequence yaml.v3 writes for the text
	// above when the []byte is left to itself.
	require.NotContains(t, stored, "- 115")
	require.NotRegexp(t, `(?m)^\s+- \d+$`, stored, "no file may be stored as a sequence of decimal bytes")

	back, err := decodeRelease(fb.configs[releaseConfigName("demo", 1)].data)
	require.NoError(t, err)
	require.Equal(t, map[string][]byte{"files/nginx.conf": text, "files/tls/ca.der": binary},
		map[string][]byte(back.Files))
}

// A release with no files must store exactly what it stored before this
// encoding existed: no files: key at all. The literal below was captured from
// the code as it was before the change, so this is a byte-identical comparison
// and not a restatement of what the encoder now happens to do.
//
// omitempty gets there for nil and for an empty map alike — yaml.v3 tests the
// map's length before it ever calls MarshalYAML — and both must decode back to
// nil, or a rollback of a fileless release would deploy an empty map where the
// original deploy sent nothing.
func TestAFilelessRecordIsUnchangedByTheFileEncoding(t *testing.T) {
	const golden = `release: demo
revision: 1
status: deployed
chart:
    name: demo
    version: 0.1.0
values:
    replicas: 2
manifest: |
    services:
      web:
        image: nginx
created: "2023-11-14T22:13:20Z"
namespace: demo
owner: apply/prod:release/demo
`
	for _, tc := range []struct {
		name  string
		files releaseFiles
	}{
		{"nil", nil},
		{"empty", releaseFiles{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fb := newFakeBackend()
			e := testEngine(fb)
			rel := &Release{
				Name: "demo", Revision: 1, Status: StatusDeployed,
				Chart:     ReleaseChart{Name: "demo", Version: "0.1.0"},
				Values:    map[string]any{"replicas": 2},
				Manifest:  "services:\n  web:\n    image: nginx\n",
				Created:   "2023-11-14T22:13:20Z",
				Namespace: "demo",
				Owner:     "apply/prod:release/demo",
				Files:     tc.files,
			}
			require.NoError(t, e.storeRevision(context.Background(), rel))
			require.Equal(t, golden, storedYAML(t, fb, "demo", 1))

			back, err := decodeRelease(fb.configs[releaseConfigName("demo", 1)].data)
			require.NoError(t, err)
			require.Nil(t, back.Files)
		})
	}
}

// A stored value that is not base64 must fail loudly, naming the file.
//
// Decoding it as empty content would be the worst outcome available: "this
// release shipped an empty file" is something a record may legitimately say, so
// nothing downstream could tell corruption from truth — and a rollback would
// then deploy that emptiness over a config that was fine.
func TestACorruptStoredFileNamesTheKeyRatherThanReadingAsEmpty(t *testing.T) {
	gz, err := gzipBytes([]byte("release: demo\nrevision: 1\nfiles:\n    files/nginx.conf: \"not base64!\"\n"))
	require.NoError(t, err)

	_, err = decodeRelease(gz)
	require.ErrorContains(t, err, "files/nginx.conf")
	require.ErrorContains(t, err, "base64")
}

// encoding/json needs no equivalent of releaseFiles: it already encodes a
// []byte as a base64 string, and a named map type is a plain map to it. So
// Release's JSON shape is exactly what charts/json_test.go pins, which is why
// that file stays green untouched.
func TestReleaseFilesAreAlreadyBase64InJSON(t *testing.T) {
	body := []byte("server { listen 80; }")
	b, err := json.Marshal(Release{Name: "demo", Files: releaseFiles{"files/nginx.conf": body}})
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got))
	require.Equal(t, map[string]any{"files/nginx.conf": base64.StdEncoding.EncodeToString(body)}, got["files"])

	var back Release
	require.NoError(t, json.Unmarshal(b, &back))
	require.Equal(t, map[string][]byte{"files/nginx.conf": body}, map[string][]byte(back.Files))

	// Absent rather than null when there are none, like every other optional key.
	b, err = json.Marshal(Release{Name: "demo"})
	require.NoError(t, err)
	require.NotContains(t, string(b), "files")
}

const extNetManifest = "version: \"3.9\"\n" +
	"services:\n  app:\n    image: x\n" +
	"networks:\n  traefik-public:\n    external: true\n"

func TestInstallExternalNetworkPresent(t *testing.T) {
	fb := newFakeBackend()
	fb.networkScopes["traefik-public"] = "swarm"
	e := testEngine(fb)
	rel, err := e.Install(context.Background(), "demo", ReleaseChart{Name: "demo", Version: "1"}, nil, extNetManifest, InstallOptions{})
	require.NoError(t, err)
	require.Equal(t, StatusDeployed, rel.Status)
	require.Equal(t, extNetManifest, fb.deployed["demo"])
}

// A deploy failure must roll back any network this install auto-created, so a
// failed install leaves no trace behind.
func TestInstallRollsBackAutoCreatedNetworkOnDeployFailure(t *testing.T) {
	fb := newFakeBackend()
	fb.failNext = true // DeployStack fails
	e := testEngine(fb)
	_, err := e.Install(context.Background(), "demo", ReleaseChart{Name: "demo", Version: "1"}, nil, extNetManifest, InstallOptions{})
	require.Error(t, err)
	require.NotContains(t, fb.networkScopes, "traefik-public")
}

func TestInstallExternalNetworkAutoCreated(t *testing.T) {
	fb := newFakeBackend()
	e := testEngine(fb)
	rel, err := e.Install(context.Background(), "demo", ReleaseChart{Name: "demo", Version: "1"}, nil, extNetManifest, InstallOptions{})
	require.NoError(t, err)
	require.Equal(t, StatusDeployed, rel.Status)
	require.Equal(t, "swarm", fb.networkScopes["traefik-public"], "missing external network should be auto-created swarm-scoped")
	require.Equal(t, extNetManifest, fb.deployed["demo"])
}

func TestInstallExternalNetworkCreateFails(t *testing.T) {
	fb := newFakeBackend()
	fb.createNetErr["traefik-public"] = fmt.Errorf("not a swarm manager")
	e := testEngine(fb)
	rel, err := e.Install(context.Background(), "demo", ReleaseChart{Name: "demo", Version: "1"}, nil, extNetManifest, InstallOptions{})
	require.Error(t, err)
	require.Equal(t, StatusFailed, rel.Status)
	require.Contains(t, err.Error(), "docker network create --driver overlay --attachable traefik-public")
	require.Empty(t, fb.deployed, "deploy must not run when an external network is unavailable")
	require.Empty(t, fb.configs, "nothing should be recorded")
}

func TestInstallExternalNetworkScopeClash(t *testing.T) {
	fb := newFakeBackend()
	fb.networkScopes["traefik-public"] = "local"
	e := testEngine(fb)
	_, err := e.Install(context.Background(), "demo", ReleaseChart{Name: "demo", Version: "1"}, nil, extNetManifest, InstallOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "non-swarm")
	require.Empty(t, fb.deployed)
	require.Empty(t, fb.configs)
}

func TestListReturnsCurrentRevisions(t *testing.T) {
	fb := newFakeBackend()
	e := testEngine(fb)
	ctx := context.Background()
	_, _ = e.Install(ctx, "a", ReleaseChart{Name: "ca", Version: "1"}, nil, "services:\n  s:\n    image: x\n", InstallOptions{})
	_, _ = e.Install(ctx, "b", ReleaseChart{Name: "cb", Version: "1"}, nil, "services:\n  s:\n    image: x\n", InstallOptions{})

	list, err := e.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, "a", list[0].Name)
	require.Equal(t, "b", list[1].Name)
}

// AllRevisions is List's counterpart for a caller that needs the current
// revision of every release AND the history of any of them from one read. The
// two differ in exactly two ways, and both are the point: nothing is filtered,
// and lower revisions carry their derived status.
func TestAllRevisionsReturnsEveryRevisionUnfiltered(t *testing.T) {
	fb := newFakeBackend()
	e := testEngine(fb)
	ctx := context.Background()

	_, err := e.Install(ctx, "a", ReleaseChart{Name: "ca", Version: "1"}, nil, "services:\n  s:\n    image: x\n", InstallOptions{})
	require.NoError(t, err)
	_, err = e.Upgrade(ctx, "a", ReleaseChart{Name: "ca", Version: "2"}, nil, "services:\n  s:\n    image: y\n", InstallOptions{})
	require.NoError(t, err)
	_, err = e.Install(ctx, "b", ReleaseChart{Name: "cb", Version: "1"}, nil, "services:\n  s:\n    image: x\n", InstallOptions{})
	require.NoError(t, err)

	// A record another producer left in StatusUninstalled. List hides it;
	// AllRevisions must not, because a caller asking for every revision is
	// asking about the record, not about what is deployed.
	gone := &Release{Name: "c", Revision: 1, Status: StatusUninstalled, Chart: ReleaseChart{Name: "cc", Version: "1"}}
	fb.configs[releaseConfigName("c", 1)] = fakeConfig{
		data:   mustGzipRelease(t, gone),
		labels: map[string]string{LabelType: TypeRelease},
	}

	all, err := e.AllRevisions(ctx)
	require.NoError(t, err)
	require.Len(t, all, 3)

	require.Len(t, all["a"], 2)
	require.Equal(t, 1, all["a"][0].Revision, "revisions are ascending")
	require.Equal(t, 2, all["a"][1].Revision)
	require.Equal(t, StatusSuperseded, all["a"][0].Status, "a lower deployed revision reads as superseded")
	require.Equal(t, StatusDeployed, all["a"][1].Status)

	require.Len(t, all["b"], 1)
	require.Len(t, all["c"], 1)
	require.Equal(t, StatusUninstalled, all["c"][0].Status)

	list, err := e.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2, "List still hides the uninstalled release AllRevisions reports")
}

// Reading release history is the engine's hottest path — every List, Status,
// Install, Upgrade, Rollback and GetRevision walks it, and a GitOps controller
// walks it on a timer. When the Backend's listing carries the payload, that walk
// is one API call rather than one per stored revision (issue #510).
func TestAllRevisionsReadsThePayloadFromTheListing(t *testing.T) {
	fb := newFakeBackend()
	fb.listData = true
	e := testEngine(fb)
	ctx := context.Background()
	_, err := e.Install(ctx, "a", ReleaseChart{Name: "ca", Version: "1"}, nil, "services:\n  s:\n    image: x\n", InstallOptions{})
	require.NoError(t, err)
	_, err = e.Upgrade(ctx, "a", ReleaseChart{Name: "ca", Version: "2"}, nil, "services:\n  s:\n    image: y\n", InstallOptions{})
	require.NoError(t, err)

	fb.inspects = 0
	list, err := e.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, 2, list[0].Revision, "the payload from the listing must decode to the same release")
	require.Zero(t, fb.inspects, "the payload was in the listing; inspecting again is a wasted round trip per revision")
}

// A Backend that lists names and labels but not payloads stays correct: the
// field is additive, so an implementation written before it existed keeps
// working through the inspect it always did.
func TestAllRevisionsInspectsWhenTheListingOmitsThePayload(t *testing.T) {
	fb := newFakeBackend() // listData off
	e := testEngine(fb)
	ctx := context.Background()
	_, err := e.Install(ctx, "a", ReleaseChart{Name: "ca", Version: "1"}, nil, "services:\n  s:\n    image: x\n", InstallOptions{})
	require.NoError(t, err)

	fb.inspects = 0
	list, err := e.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, 1, list[0].Revision)
	require.Equal(t, 1, fb.inspects, "the payload was not in the listing, so it had to be inspected")
}

func TestUninstallRemovesStackAndConfigsKeepsVolumes(t *testing.T) {
	fb := newFakeBackend()
	fb.volumes["demo"] = []string{"demo_data"}
	e := testEngine(fb)
	ctx := context.Background()
	_, err := e.Install(ctx, "demo", ReleaseChart{Name: "demo", Version: "1"}, nil, "services:\n  s:\n    image: x\n", InstallOptions{})
	require.NoError(t, err)

	_, err = e.Uninstall(ctx, "demo", false)
	require.NoError(t, err)
	require.Empty(t, fb.deployed)
	require.Empty(t, fb.configs)
	require.Equal(t, []string{"demo_data"}, fb.volumes["demo"]) // volume retained

	_, err = e.Uninstall(ctx, "demo", false) // already gone
	require.Error(t, err)
}

func TestUninstallPurgeVolumes(t *testing.T) {
	fb := newFakeBackend()
	fb.volumes["demo"] = []string{"demo_data"}
	e := testEngine(fb)
	ctx := context.Background()
	_, _ = e.Install(ctx, "demo", ReleaseChart{Name: "demo", Version: "1"}, nil, "services:\n  s:\n    image: x\n", InstallOptions{})
	_, err := e.Uninstall(ctx, "demo", true)
	require.NoError(t, err)
	require.Empty(t, fb.volumes["demo"])
}

func TestStatusReturnsServices(t *testing.T) {
	fb := newFakeBackend()
	fb.services["demo"] = []ServiceState{{Name: "app", Mode: "replicated", Replicas: "1/1", Status: "running"}}
	e := testEngine(fb)
	ctx := context.Background()
	_, _ = e.Install(ctx, "demo", ReleaseChart{Name: "demo", Version: "1"}, nil, "services:\n  s:\n    image: x\n", InstallOptions{})
	rel, svcs, err := e.Status(ctx, "demo")
	require.NoError(t, err)
	require.Equal(t, 1, rel.Revision)
	require.Len(t, svcs, 1)
	require.Equal(t, "app", svcs[0].Name)
}

func mustGzipRelease(t *testing.T, rel *Release) []byte {
	t.Helper()
	payload, err := yaml.Marshal(rel)
	require.NoError(t, err)
	gz, err := gzipBytes(payload)
	require.NoError(t, err)
	return gz
}

// stableAge outlives any monitor window these tests declare, so a service
// carrying it has already served out its stability window and parity is the only
// thing left to decide.
const stableAge = time.Hour

func phaseOf(s ServiceState) Phase { return s.Convergence().Phase }

// A service at parity whose update is still in flight must not count as ready.
func TestConvergenceRejectsInProgress(t *testing.T) {
	require.Equal(t, PhaseConverged, phaseOf(ServiceState{Running: 2, Desired: 2, NewestTaskAge: stableAge}))
	require.Equal(t, PhaseConverged, phaseOf(ServiceState{Running: 1, Desired: 1, UpdateState: "completed", NewestTaskAge: stableAge}))
	require.Equal(t, PhaseProgressing, phaseOf(ServiceState{Running: 2, Desired: 2, UpdateState: "updating", NewestTaskAge: stableAge}))
	require.Equal(t, PhaseProgressing, phaseOf(ServiceState{Running: 1, Desired: 1, UpdateState: "rollback_started", NewestTaskAge: stableAge}))
	require.Equal(t, PhaseProgressing, phaseOf(ServiceState{Running: 1, Desired: 2, NewestTaskAge: stableAge}))
}

// The bug in #473: a fresh install has no UpdateStatus, so the in-flight guard
// cannot fire, and the old check compared a Replicas string counting tasks by
// DESIRED state — which reaches parity the moment swarm schedules them. A
// release whose tasks are all still pending must not read as converged.
func TestConvergenceRejectsScheduledButNotRunning(t *testing.T) {
	scheduled := ServiceState{
		Name:          "api",
		Replicas:      "3/3", // what the old check saw: 3 tasks DESIRED running
		Status:        "active",
		Running:       0, // ...none of which had actually started
		Desired:       3,
		UpdateState:   "", // fresh install: never updated
		NewestTaskAge: stableAge,
	}
	c := scheduled.Convergence()
	require.Equal(t, PhaseProgressing, c.Phase, "tasks that are merely scheduled must not count as converged")
	require.Equal(t, "0/3 tasks running", c.Reason)
}

// The bug in #443: a one-shot init or migration step ends with its task
// Complete and nothing running, which is its SUCCESS state. Requiring a running
// task held --wait until the timeout for a job that had already done its work,
// and showed the release as 0/1 forever.
func TestConvergenceAcceptsACompletedJob(t *testing.T) {
	done := ServiceState{
		Name:          "superset_init",
		Running:       0, // the task exited; swarm will not replace it
		Completed:     1,
		Desired:       1,
		Job:           true,
		NewestTaskAge: stableAge,
	}
	require.Equal(t, PhaseConverged, phaseOf(done), "a job that ran to completion is converged")

	// A job whose task has not finished yet is still not converged.
	require.Equal(t, PhaseProgressing, phaseOf(ServiceState{Running: 0, Completed: 0, Desired: 1, Job: true, NewestTaskAge: stableAge}))

	// The distinction that matters: a task that failed ends in state Failed, so
	// it is never counted as Completed and a broken job still blocks the wait.
	require.Equal(t, PhaseProgressing, phaseOf(ServiceState{Running: 0, Completed: 0, Desired: 1, Job: true, Status: "active", NewestTaskAge: stableAge}))
}

// A native job (replicated-job, global-job) targets completions, so a running
// task is progress, not arrival: --wait holds until the job has finished
// (issue #666).
func TestConvergenceWaitsForANativeJobToComplete(t *testing.T) {
	running := ServiceState{Running: 1, Completed: 2, Desired: 3, Job: true, NativeJob: true, NewestTaskAge: stableAge}.Convergence()
	require.Equal(t, PhaseProgressing, running.Phase, "the last task is still running")
	require.Equal(t, "2/3 tasks completed", running.Reason)

	done := ServiceState{Completed: 3, Desired: 3, Job: true, NativeJob: true, NewestTaskAge: stableAge}
	require.Equal(t, PhaseConverged, phaseOf(done))
}

// A one-shot's task ends inside the monitor window by design, and swarmkit
// counts any task leaving RUNNING there as an update failure with no exemption
// for a restart policy of none. So the default failure_action leaves EVERY
// re-run job paused, and reading that as wedged reported a migration that had
// done its work as a stack needing manual recovery — the state both
// eldara-swarmcli stacks were in. Verified against a real swarm: a stack
// redeployed with an exit-0 one-shot leaves UpdateStatus "update paused due to
// failure or early termination of task ...".
func TestConvergenceIgnoresThePauseAJobCannotAvoid(t *testing.T) {
	done := ServiceState{
		Name:          "migrate",
		Completed:     1,
		Desired:       1,
		Job:           true,
		UpdateState:   "paused",
		NewestTaskAge: stableAge,
	}
	require.Equal(t, PhaseConverged, phaseOf(done), "the pause is the job finishing, not a verdict on it")

	// A long-running service that paused is still wedged: it has no reason to
	// end a task, so the pause means what it says.
	require.Equal(t, PhaseWedged, phaseOf(ServiceState{Running: 1, Desired: 1, UpdateState: "paused", NewestTaskAge: stableAge}))
}

// The other half: a job whose task did NOT complete leaves the service paused
// in exactly the same way, so the two cases must be told apart by the task. The
// message names the count, since "update paused after a task failure" is the one
// sentence a one-shot produces whether it worked or not.
func TestConvergenceReportsAJobThatDidNotComplete(t *testing.T) {
	c := ServiceState{
		Name:          "migrate",
		Completed:     0,
		Desired:       1,
		Job:           true,
		UpdateState:   "paused",
		NewestTaskAge: stableAge,
	}.Convergence()
	require.Equal(t, PhaseWedged, c.Phase)
	require.Equal(t, "the one-shot task did not complete (0/1); swarm paused the rollout", c.Reason)
}

// A one-shot swarm will not retry, whose task ended without completing, has
// failed — and the reason belongs in the reason, since "0/1 tasks running" is
// what a release that is merely slow says too (issue #651).
func TestConvergenceReportsATaskSwarmWillNotRetry(t *testing.T) {
	c := ServiceState{
		Name:           "migrate",
		Desired:        1,
		Job:            true,
		DeadTask:       true,
		DeadTaskReason: "task: non-zero exit (3)",
		NewestTaskAge:  stableAge,
	}.Convergence()
	require.Equal(t, PhaseWedged, c.Phase)
	require.Equal(t, "the one-shot task did not complete and swarm will not retry it: task: non-zero exit (3)", c.Reason)

	// Swarm records no message for every terminal state, so the verdict must not
	// depend on having one.
	silent := ServiceState{Desired: 1, Job: true, DeadTask: true, NewestTaskAge: stableAge}.Convergence()
	require.Equal(t, PhaseWedged, silent.Phase)
	require.Equal(t, "the one-shot task did not complete and swarm will not retry it", silent.Reason)

	// A dead task from an earlier generation must not bury a job that has since
	// met its target.
	done := ServiceState{Desired: 1, Completed: 1, Job: true, DeadTask: true, NewestTaskAge: stableAge}
	require.Equal(t, PhaseConverged, phaseOf(done))
}

// A global service on a drained node lowers the target rather than leaving the// A global service on a drained node lowers the target rather than leaving the
// release permanently short of a replica that can never be scheduled.
func TestConvergenceGlobalTracksActiveNodes(t *testing.T) {
	require.Equal(t, PhaseConverged, phaseOf(ServiceState{Mode: "global", Running: 2, Desired: 2, NewestTaskAge: stableAge}))
	require.Equal(t, PhaseProgressing, phaseOf(ServiceState{Mode: "global", Running: 1, Desired: 2, NewestTaskAge: stableAge}))
}

// Parity is necessary but not sufficient: a task that starts and then dies
// inside the monitor window is a rollout failure, so a service that has reached
// its target but not yet served out the window is still progressing.
func TestConvergenceHoldsUntilTheStabilityWindowCloses(t *testing.T) {
	c := ServiceState{Running: 1, Desired: 1, Monitor: 90 * time.Second, NewestTaskAge: 60 * time.Second}.Convergence()
	require.Equal(t, PhaseProgressing, c.Phase)
	require.Equal(t, "30s of the stability window remains", c.Reason)

	require.Equal(t, defaultStabilityWindow, ServiceState{Monitor: time.Second}.stabilityRemaining(),
		"a shorter monitor must not weaken the default")
}

// The window is measured from task creation, the same instant swarm measures it
// from — so a task that has already lived through it is stable NOW.
//
// This is the whole point. A task only reports running once its healthcheck
// passes, so start_period and the checks that followed are already spent by the
// time parity is observed. Restarting a full window at parity made --wait sit
// out the monitor period on top of however long the service took to come up,
// which is strictly more conservative than swarm and turns an honest
// `monitor: 8m` into an eight-minute install.
func TestStabilityRemainingCountsTimeTheTaskHasAlreadyLived(t *testing.T) {
	monitor := 90 * time.Second

	require.Equal(t, 30*time.Second,
		ServiceState{Monitor: monitor, NewestTaskAge: 60 * time.Second}.stabilityRemaining())
	require.LessOrEqual(t, ServiceState{Monitor: monitor, NewestTaskAge: monitor}.stabilityRemaining(),
		time.Duration(0), "a task that has outlived its monitor is already stable")
	require.LessOrEqual(t, ServiceState{Monitor: monitor, NewestTaskAge: 10 * time.Minute}.stabilityRemaining(),
		time.Duration(0), "an over-aged task must not produce a negative wait either")
}

// paused and rollback_paused are terminal: swarmkit never rolls back a
// rollback, so waiting out the timeout only delays the report.
func TestConvergenceWedged(t *testing.T) {
	require.Equal(t, PhaseProgressing, phaseOf(ServiceState{Name: "api", UpdateState: "updating"}))
	require.Equal(t, PhaseProgressing, phaseOf(ServiceState{Name: "api", UpdateState: ""}))

	c := Rollup([]ServiceState{{Name: "api", UpdateState: "paused"}})
	require.Equal(t, PhaseWedged, c.Phase)
	require.Contains(t, c.Reason, "api")

	c = Rollup([]ServiceState{{Name: "api", UpdateState: "rollback_paused"}})
	require.Equal(t, PhaseWedged, c.Phase)
	require.Contains(t, c.Reason, "manual recovery")
}

// The bug in #526: swarmkit restores the previous spec BEFORE it marks the
// rollback complete, so at "rollback_completed" the tasks that reach parity are
// running exactly what the deploy set out to replace. Falling through to the
// parity check let a failed rollout serve out the stability window and report
// converged — --wait returned nil and a pipeline gating on it went green.
func TestConvergenceRejectsACompletedRollback(t *testing.T) {
	rolledBack := ServiceState{Name: "api", Running: 1, Desired: 1, UpdateState: "rollback_completed", NewestTaskAge: stableAge}

	c := rolledBack.Convergence()
	require.Equal(t, PhaseWedged, c.Phase, "a deploy that was rolled back is a failed deploy, not a converged one")
	require.Contains(t, c.Reason, "rolled back")

	// And --wait says so instead of returning nil.
	prev := waitPollInterval
	waitPollInterval = time.Millisecond
	defer func() { waitPollInterval = prev }()

	b := &convergenceBackend{states: []ServiceState{rolledBack}}
	e := &Engine{Backend: b, now: time.Now}
	require.ErrorContains(t, e.waitReady(context.Background(), "rel", time.Hour), "rolled back")
	require.Equal(t, 1, b.calls, "should fail on the first observation, not wait out the timeout")
}

// The rollup is what a caller reads, so the phase that should worry it most has
// to win regardless of where in the list it appears — one wedged service must
// not be masked by another that is merely slow.
func TestRollupTakesTheWorstPhase(t *testing.T) {
	states := []ServiceState{
		{Name: "web", Running: 1, Desired: 1, NewestTaskAge: stableAge},
		{Name: "api", Running: 0, Desired: 1, NewestTaskAge: stableAge},
		{Name: "db", UpdateState: "paused"},
	}
	c := Rollup(states)
	require.Equal(t, PhaseWedged, c.Phase)
	require.Contains(t, c.Reason, `service 'db'`)

	// Progressing wins over converged the same way, and names the service.
	c = Rollup(states[:2])
	require.Equal(t, PhaseProgressing, c.Phase)
	require.Equal(t, `service 'api': 0/1 tasks running`, c.Reason)

	require.Equal(t, PhaseConverged, Rollup(states[:1]).Phase)
}

// A release whose stack reports nothing has not finished coming up. Reporting it
// converged would let --wait return the instant a deploy was accepted.
func TestRollupOfNoServicesIsProgressing(t *testing.T) {
	require.Equal(t, PhaseProgressing, Rollup(nil).Phase)
}

// When a concurrent actor claims the next revision number between read and
// write, record() must re-read the history and bump to the next free revision
// rather than colliding.
func TestRecordRetriesOnRevisionCollision(t *testing.T) {
	fb := newFakeBackend()
	e := testEngine(fb)
	ctx := context.Background()

	_, err := e.Install(ctx, "demo", ReleaseChart{Name: "demo", Version: "1"}, nil, "services:\n  a:\n    image: x\n", InstallOptions{})
	require.NoError(t, err)

	// Simulate another actor recording v2 first: the create for v2 collides and
	// the colliding revision becomes visible, so the retry must allocate v3.
	fb.onCreate = func(name string) error {
		if name == releaseConfigName("demo", 2) {
			fb.onCreate = nil
			fb.configs[name] = fakeConfig{
				data:   mustGzipRelease(t, &Release{Name: "demo", Revision: 2, Status: StatusDeployed, Chart: ReleaseChart{Name: "demo", Version: "1"}}),
				labels: map[string]string{LabelType: TypeRelease, LabelRelease: "demo", LabelRevision: "2"},
			}
			return fmt.Errorf("config %q already exists", name)
		}
		return nil
	}
	rel, err := e.Upgrade(ctx, "demo", ReleaseChart{Name: "demo", Version: "2"}, nil, "services:\n  a:\n    image: y\n", InstallOptions{})
	require.NoError(t, err)
	require.Equal(t, 3, rel.Revision)
}

// A stack-removal failure during uninstall must not strand the release history:
// cleanup continues and the aggregated error is still reported.
func TestUninstallContinuesOnPartialFailure(t *testing.T) {
	fb := newFakeBackend()
	e := testEngine(fb)
	ctx := context.Background()
	_, err := e.Install(ctx, "demo", ReleaseChart{Name: "demo", Version: "1"}, nil, "services:\n  a:\n    image: x\n", InstallOptions{})
	require.NoError(t, err)

	fb.rmStackErr = fmt.Errorf("stack gone")
	_, err = e.Uninstall(ctx, "demo", false)
	require.ErrorContains(t, err, "removing stack")
	require.Empty(t, fb.configs) // history cleaned up despite the stack-removal failure
}

// waitReady must hold parity for the stability window before reporting success:
// a task that starts and then dies inside UpdateConfig.Monitor is a rollout
// failure, so returning at first parity reports success for a service that is
// about to crash-loop.
func TestWaitReadyHoldsTheStabilityWindow(t *testing.T) {
	prev := waitPollInterval
	waitPollInterval = time.Millisecond
	defer func() { waitPollInterval = prev }()

	b := &convergenceBackend{states: []ServiceState{{Name: "api", Running: 1, Desired: 1}}}

	// The clock advances far too slowly to clear the 5s stability window, but
	// fast enough to reach a 50ms deadline. Parity is immediate, so a waitReady
	// that returned at first parity would report success here.
	clock := time.Now()
	e := &Engine{Backend: b, now: func() time.Time {
		clock = clock.Add(time.Millisecond)
		return clock
	}}

	err := e.waitReady(context.Background(), "rel", 50*time.Millisecond)
	require.Error(t, err)
	require.Contains(t, err.Error(), "timed out")
	require.Greater(t, b.calls, 1, "should keep polling rather than returning at first parity")
}

func TestWaitReadyReturnsOnceTheWindowElapses(t *testing.T) {
	prev := waitPollInterval
	waitPollInterval = time.Millisecond
	defer func() { waitPollInterval = prev }()

	young := []ServiceState{{Name: "api", Running: 1, Desired: 1, NewestTaskAge: time.Second}}
	aged := []ServiceState{{Name: "api", Running: 1, Desired: 1, NewestTaskAge: time.Minute}}

	b := &scriptedBackend{script: [][]ServiceState{young, young, aged}}
	e := &Engine{Backend: b, now: time.Now}

	require.NoError(t, e.waitReady(context.Background(), "rel", time.Hour))
	require.Equal(t, 3, b.calls, "must wait for the task to outlive the window, not return at parity")
}

// A replacement task after a lost rollout is newer, so the window it has left
// grows back and waitReady must keep waiting rather than treating regained
// parity as already-stable.
func TestWaitReadyWaitsOutAReplacementTasksFreshWindow(t *testing.T) {
	prev := waitPollInterval
	waitPollInterval = time.Millisecond
	defer func() { waitPollInterval = prev }()

	young := []ServiceState{{Name: "api", Running: 1, Desired: 1, NewestTaskAge: time.Second}}
	lost := []ServiceState{{Name: "api", Running: 0, Desired: 1}}
	aged := []ServiceState{{Name: "api", Running: 1, Desired: 1, NewestTaskAge: time.Minute}}

	b := &scriptedBackend{script: [][]ServiceState{young, lost, young, aged}}
	e := &Engine{Backend: b, now: time.Now}

	require.NoError(t, e.waitReady(context.Background(), "rel", time.Hour))
	require.Equal(t, 4, b.calls, "the fresh task's remaining window must still be waited out")
}

// A wedged rollout is reported immediately rather than after the full timeout.
func TestWaitReadyFailsFastOnWedgedRollout(t *testing.T) {
	prev := waitPollInterval
	waitPollInterval = time.Millisecond
	defer func() { waitPollInterval = prev }()

	b := &convergenceBackend{states: []ServiceState{{Name: "api", Running: 0, Desired: 1, UpdateState: "paused"}}}
	e := &Engine{Backend: b, now: time.Now}

	err := e.waitReady(context.Background(), "rel", time.Hour)
	require.Error(t, err)
	require.Contains(t, err.Error(), "update paused")
	require.Equal(t, 1, b.calls, "should fail on the first observation, not wait out the timeout")
}

// A cancelled wait ends at once, with the context's own error. The timeout it
// would otherwise serve out defaults to five minutes and is legitimately set
// higher, so a caller with a shutdown grace period measured in seconds cannot
// wait for it — and the release may be one that never converges at all.
func TestWaitReadyReturnsOnACancelledContext(t *testing.T) {
	// Long enough that nothing but the cancellation can end the sleep, and a
	// timeout to match, so the wait has exactly one way out.
	prev := waitPollInterval
	waitPollInterval = time.Hour
	defer func() { waitPollInterval = prev }()

	b := &convergenceBackend{states: []ServiceState{{Name: "api", Running: 0, Desired: 1}}}
	e := &Engine{Backend: b, now: time.Now}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { done <- e.waitReady(ctx, "rel", time.Hour) }()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled, "the wait must report the cancellation, not a timeout")
	case <-time.After(5 * time.Second):
		t.Fatal("waitReady ignored the cancelled context and sat on its poll interval")
	}
}

// convergenceBackend is a Backend that only answers StackServices; waitReady
// touches nothing else.
type convergenceBackend struct {
	Backend
	states []ServiceState
	calls  int
}

func (b *convergenceBackend) StackServices(context.Context, string) []ServiceState {
	b.calls++
	return b.states
}

// scriptedBackend returns a different state per poll, then repeats the last.
type scriptedBackend struct {
	Backend
	script [][]ServiceState
	calls  int
}

func (b *scriptedBackend) StackServices(context.Context, string) []ServiceState {
	i := b.calls
	b.calls++
	if i >= len(b.script) {
		i = len(b.script) - 1
	}
	return b.script[i]
}
