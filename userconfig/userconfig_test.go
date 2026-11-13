// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package userconfig

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// useDir points XDG_CONFIG_HOME at a fresh directory and returns the
// config.yaml path inside it, which does not exist yet.
func useDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return filepath.Join(dir, "swarmcli", "config.yaml")
}

func writeConfig(t *testing.T, p, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}

func read(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	require.NoError(t, err)
	return string(data)
}

// skipAsRoot skips a test that relies on a permission being refused.
func skipAsRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root is never refused")
	}
}

func TestPath_XDGConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	p, err := Path()
	require.NoError(t, err)
	require.Equal(t, "/xdg/swarmcli/config.yaml", p)
}

// ~/.config on every OS, macOS included, and a relative XDG_CONFIG_HOME counts
// as unset.
func TestPath_HomeWhenXDGIsUnsetOrRelative(t *testing.T) {
	t.Setenv("HOME", "/home/someone")
	for _, xdg := range []string{"", "relative/dir"} {
		t.Setenv("XDG_CONFIG_HOME", xdg)
		p, err := Path()
		require.NoError(t, err)
		require.Equal(t, "/home/someone/.config/swarmcli/config.yaml", p, "XDG_CONFIG_HOME=%q", xdg)
	}
}

func TestPath_NoHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	_, err := Path()
	require.Error(t, err)
}

func TestLoad_NoFileIsNoPreference(t *testing.T) {
	useDir(t)
	c, err := load()
	require.NoError(t, err)
	require.Equal(t, Config{}, c)
}

func TestLoad_NoHomeIsNoPreference(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	c, err := load()
	require.NoError(t, err)
	require.Equal(t, Config{}, c)
}

func TestLoad_ReadsTheKeys(t *testing.T) {
	p := useDir(t)
	writeConfig(t, p, "mouse: false\ntelemetry: ' OFF '\n")

	c, err := load()
	require.NoError(t, err)
	require.NotNil(t, c.Mouse)
	require.False(t, *c.Mouse)
	require.Equal(t, "off", c.Telemetry)
}

func TestLoad_MouseSpellings(t *testing.T) {
	p := useDir(t)
	for value, want := range map[string]bool{
		"true": true, "yes": true, "on": true, "1": true, "TRUE": true,
		"false": false, "no": false, "off": false, "0": false,
	} {
		writeConfig(t, p, "mouse: "+value+"\n")
		c, err := load()
		require.NoError(t, err)
		require.NotNil(t, c.Mouse, "mouse: %s", value)
		require.Equal(t, want, *c.Mouse, "mouse: %s", value)
	}
}

// A bad value is that key's default, and an unknown key is ignored; neither
// stops the file from being read.
func TestLoad_BadValuesAndUnknownKeysAreNotFatal(t *testing.T) {
	p := useDir(t)
	for _, content := range []string{
		"mouse: maybe\ntheme: dark\n",
		"mouse: [true]\nfuture:\n  nested: 1\n",
	} {
		writeConfig(t, p, content)
		c, err := load()
		require.NoError(t, err, content)
		require.Nil(t, c.Mouse, content)
	}
}

func TestLoad_EmptyCommentOnlyAndNullFilesHoldNothing(t *testing.T) {
	p := useDir(t)
	for _, content := range []string{"", "# nothing yet\n", "---\n", "~\n"} {
		writeConfig(t, p, content)
		c, err := load()
		require.NoError(t, err, "%q", content)
		require.Equal(t, Config{}, c, "%q", content)
	}
}

func TestLoad_UnparseableIsAnError(t *testing.T) {
	p := useDir(t)
	for _, content := range []string{"mouse: [\n", "- mouse\n- telemetry\n", "just words\n"} {
		writeConfig(t, p, content)
		c, err := load()
		require.ErrorIs(t, err, ErrUnparseable, "%q", content)
		require.ErrorContains(t, err, p, "the error names the file")
		require.Equal(t, Config{}, c)
	}
}

func TestLoad_UnreadableIsAnError(t *testing.T) {
	p := useDir(t)
	require.NoError(t, os.MkdirAll(p, 0o755)) // a directory where the file should be

	_, err := load()
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrUnparseable)
}

func resetStartup(t *testing.T) {
	t.Helper()
	startupOnce = sync.Once{}
	t.Cleanup(func() { startupOnce = sync.Once{} })
}

// The session acts on the file as it started: a later write is for the next
// start.
func TestStartup_ReadsOnce(t *testing.T) {
	resetStartup(t)
	p := useDir(t)
	writeConfig(t, p, "telemetry: none\n")

	c, err := Startup()
	require.NoError(t, err)
	require.Equal(t, "none", c.Telemetry)

	writeConfig(t, p, "telemetry: off\n")
	c, err = Startup()
	require.NoError(t, err)
	require.Equal(t, "none", c.Telemetry)
}

func TestStartup_UnparseableRunsOnDefaults(t *testing.T) {
	resetStartup(t)
	p := useDir(t)
	writeConfig(t, p, "mouse: [\n")

	c, err := Startup()
	require.ErrorIs(t, err, ErrUnparseable)
	require.Equal(t, Config{}, c)
}

// The first write lists every key, commented out at its default, except the
// one it sets.
func TestSet_FirstWriteCreatesTheTemplate(t *testing.T) {
	p := useDir(t)
	require.NoError(t, Set(KeyMouse, "false"))

	got := read(t, p)
	require.Contains(t, got, header)
	require.Contains(t, got, "\nmouse: false\n")
	require.Contains(t, got, "\n# telemetry: on\n")
	for _, k := range keys {
		for _, line := range k.doc {
			require.Contains(t, got, "# "+line+"\n")
		}
	}

	info, err := os.Stat(p)
	require.NoError(t, err)
	require.Equal(t, fs.FileMode(0o644), info.Mode().Perm())

	c, err := load()
	require.NoError(t, err)
	require.NotNil(t, c.Mouse)
	require.False(t, *c.Mouse)
	require.Empty(t, c.Telemetry, "a commented key is unset")
}

// What a person wrote survives a write: comments, key order, keys this build
// does not know, and the changed line's own comment.
func TestSet_ChangesOnlyTheOneKey(t *testing.T) {
	p := useDir(t)
	writeConfig(t, p, `# mine
theme: dark   # not yet a key
mouse: true # on, for now
telemetry: off
future:
  nested: 1
# the end
`)
	require.NoError(t, Set(KeyMouse, "false"))

	require.Equal(t, `# mine
theme: dark # not yet a key
mouse: false # on, for now
telemetry: off
future:
  nested: 1
# the end
`, read(t, p))
}

func TestSet_AppendsAnAbsentKey(t *testing.T) {
	p := useDir(t)
	writeConfig(t, p, "telemetry: none\n")
	require.NoError(t, Set(KeyMouse, "true"))
	require.Equal(t, "telemetry: none\nmouse: true\n", read(t, p))
}

// Set works from the file on disk, so an edit made while swarmcli runs is not
// lost to the next toggle.
func TestSet_KeepsAHandEditMadeBetweenWrites(t *testing.T) {
	p := useDir(t)
	require.NoError(t, Set(KeyMouse, "false"))

	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = f.WriteString("telemetry: none\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	require.NoError(t, Set(KeyMouse, "true"))

	c, err := load()
	require.NoError(t, err)
	require.True(t, *c.Mouse)
	require.Equal(t, "none", c.Telemetry)
	require.Contains(t, read(t, p), header, "the template's comments are kept too")
}

func TestSet_RefusesAFileThatDoesNotParse(t *testing.T) {
	p := useDir(t)
	const broken = "mouse: [\n# half-way through an edit\n"
	writeConfig(t, p, broken)

	require.ErrorIs(t, Set(KeyMouse, "false"), ErrUnparseable)
	require.Equal(t, broken, read(t, p), "untouched")
}

func TestSet_CommentOnlyFileKeepsItsText(t *testing.T) {
	p := useDir(t)
	for content, want := range map[string]string{
		"# notes\n": "# notes\nmouse: false\n",
		"# notes":   "# notes\nmouse: false\n",
		"":          "mouse: false\n",
	} {
		writeConfig(t, p, content)
		require.NoError(t, Set(KeyMouse, "false"))
		require.Equal(t, want, read(t, p), "%q", content)
	}
}

func TestSet_NullDocumentBecomesAMapping(t *testing.T) {
	p := useDir(t)
	writeConfig(t, p, "~\n")
	require.NoError(t, Set(KeyMouse, "false"))
	require.Equal(t, "mouse: false\n", read(t, p))
}

func TestSet_KeepsTheFileMode(t *testing.T) {
	p := useDir(t)
	writeConfig(t, p, "mouse: true\n")
	require.NoError(t, os.Chmod(p, 0o600))

	require.NoError(t, Set(KeyMouse, "false"))

	info, err := os.Stat(p)
	require.NoError(t, err)
	require.Equal(t, fs.FileMode(0o600), info.Mode().Perm())
}

// A dotfile manager's symlink stays a symlink: the file it points at changes.
func TestSet_WritesThroughASymlink(t *testing.T) {
	p := useDir(t)
	dotfiles := filepath.Join(t.TempDir(), "dotfiles")
	real := filepath.Join(dotfiles, "config.yaml")
	writeConfig(t, real, "mouse: true\n")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.Symlink(real, p))

	require.NoError(t, Set(KeyMouse, "false"))

	info, err := os.Lstat(p)
	require.NoError(t, err)
	require.Equal(t, fs.ModeSymlink, info.Mode().Type(), "still a link")
	require.Equal(t, "mouse: false\n", read(t, real))
}

// A link whose file does not exist yet is written through, not replaced.
func TestSet_WritesThroughADanglingSymlink(t *testing.T) {
	p := useDir(t)
	require.NoError(t, os.MkdirAll(filepath.Join(filepath.Dir(p), "managed"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join("managed", "config.yaml"), p))

	require.NoError(t, Set(KeyMouse, "false"))

	info, err := os.Lstat(p)
	require.NoError(t, err)
	require.Equal(t, fs.ModeSymlink, info.Mode().Type(), "still a link")
	require.Contains(t, read(t, filepath.Join(filepath.Dir(p), "managed", "config.yaml")), "\nmouse: false\n")
}

func TestSet_UnknownKey(t *testing.T) {
	useDir(t)
	require.ErrorContains(t, Set("theme", "dark"), "unknown config.yaml key 'theme'")
}

func TestSet_NoHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	require.Error(t, Set(KeyMouse, "false"))
}

func TestSet_ReportsFailures(t *testing.T) {
	skipAsRoot(t)

	t.Run("directory cannot be created", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", dir)
		require.NoError(t, os.Chmod(dir, 0o500))
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		require.Error(t, Set(KeyMouse, "false"))
	})

	t.Run("path is not a directory", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(file, nil, 0o644))
		t.Setenv("XDG_CONFIG_HOME", file)
		err := Set(KeyMouse, "false")
		require.Error(t, err)
		require.False(t, errors.Is(err, fs.ErrNotExist))
	})

	t.Run("file cannot be read", func(t *testing.T) {
		p := useDir(t)
		writeConfig(t, p, "mouse: true\n")
		require.NoError(t, os.Chmod(p, 0o000))
		require.Error(t, Set(KeyMouse, "false"))
	})

	t.Run("directory is read-only", func(t *testing.T) {
		p := useDir(t)
		writeConfig(t, p, "mouse: true\n")
		require.NoError(t, os.Chmod(filepath.Dir(p), 0o500))
		t.Cleanup(func() { _ = os.Chmod(filepath.Dir(p), 0o700) })
		require.Error(t, Set(KeyMouse, "false"))
		require.Equal(t, "mouse: true\n", read(t, p))
	})
}

// A failed rename leaves the original and no temporary file behind.
func TestSet_FailedRenameLeavesNoTrace(t *testing.T) {
	p := useDir(t)
	writeConfig(t, p, "mouse: true\n")
	renameFn = func(string, string) error { return errors.New("disk full") }
	t.Cleanup(func() { renameFn = os.Rename })

	require.ErrorContains(t, Set(KeyMouse, "false"), "disk full")

	require.Equal(t, "mouse: true\n", read(t, p))
	entries, err := os.ReadDir(filepath.Dir(p))
	require.NoError(t, err)
	require.Len(t, entries, 1, "no temporary file left behind")
}
