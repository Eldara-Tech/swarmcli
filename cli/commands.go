// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package cli

import (
	"sort"
	"strings"

	"github.com/Eldara-Tech/swarmcli/v2/registry"
)

// chartsCmd describes one `swarmcli charts` subcommand: what it is called, what
// it takes, and what it does. The table below is the single source of truth
// behind dispatch, the usage text and the generated command blocks in
// README.md and charts/README.md — three places that each used to carry their
// own hand-maintained copy of the same list, and drifted.
//
// Adding a subcommand means adding a row here. Nothing else has a list to
// update.
type chartsCmd struct {
	// Name is the word after `swarmcli charts`, and the key dispatch looks up.
	Name string
	// Aliases are alternative spellings dispatch accepts. They are reported in
	// the rendered summary rather than given lines of their own.
	Aliases []string
	// Group is the heading this command is listed under. Rendering follows
	// chartGroups order, not declaration order.
	Group string
	// Usage is one or more display lines. A command with sub-verbs (`repo add`,
	// `show values`) lists each of them here: they are one dispatch target but
	// several things an operator can run.
	Usage []cmdUsage
	// Flags is the allow-list: every flag this command actually reads, in long
	// form. A flag outside it is rejected rather than parsed and dropped —
	// derived by reading the handler and everything it calls (newStore reads
	// --no-repo-update, loadChart adds --version, prepare adds the values and
	// compat flags), because the flag struct is shared and reading the handler
	// alone understates what it honours.
	Flags []string
	// FlagHint, when set, is appended to a rejection so the operator is told
	// what to do instead of what not to do.
	FlagHint string
	// Run is the handler. It receives the arguments after the command name.
	Run func(chartsCmd, []string) int
}

// cmdUsage is one rendered line: the arguments as an operator types them, and
// what that invocation does.
type cmdUsage struct {
	Args    string
	Summary string
}

// chartGroups is the order the groups appear in, which is roughly the order an
// operator meets them: configure a repository, find a chart, write one, deploy
// it, then automate the deployment.
var chartGroups = []string{"Repository", "Discovery", "Authoring", "Releases", "GitOps"}

// chartsCommands is the command set. Ordering within a group is the order rows
// appear here.
var chartsCommands = []chartsCmd{
	{
		Name:  "repo",
		Group: "Repository",
		Usage: []cmdUsage{
			{"add <name> <url>", "Add a chart repository and download its index"},
			{"list", "List configured repositories"},
			{"update [name]", "Refresh repository indexes (all, or one)"},
			{"remove <name>", "Remove a repository"},
		},
		Run: chartsRepo,
	},
	{
		Name:  "search",
		Group: "Discovery",
		Usage: []cmdUsage{{"[keyword]", "Search charts across repositories"}},
		Flags: []string{"--no-repo-update"},
		Run:   chartsSearch,
	},
	{
		Name:  "show",
		Group: "Discovery",
		Usage: []cmdUsage{
			{"chart <repo/chart>", "Show chart metadata"},
			{"values <repo/chart>", "Show default values.yaml"},
			{"schema <repo/chart>", "Show values.schema.json"},
		},
		Flags: []string{"--version", "--skip-compat-check", "--no-repo-update"},
		Run:   chartsShow,
	},
	{
		Name:  "lint",
		Group: "Authoring",
		Usage: []cmdUsage{{"<chart>", "Check a chart without deploying it"}},
		Flags: []string{"--values", "--set", "--version", "--for-version", "--no-repo-update"},
		Run:   chartsLint,
	},
	{
		Name:  "template",
		Group: "Releases",
		Usage: []cmdUsage{{"<release> <chart>", "Render manifest to stdout (no deploy)"}},
		Flags: []string{
			"--values",
			"--set",
			"--set-file",
			"--version",
			"--requirements",
			"--skip-compat-check",
			"--no-repo-update",
		},
		Run: chartsTemplate,
	},
	{
		Name:  "install",
		Group: "Releases",
		Usage: []cmdUsage{{"<release> <chart>", "Install a chart as a release"}},
		Flags: []string{
			"--values",
			"--set",
			"--set-file",
			"--version",
			"--dry-run",
			"--wait",
			"--timeout",
			"--history-max",
			"--resolve-image",
			"--skip-compat-check",
			"--no-repo-update",
		},
		Run: chartsInstall,
	},
	{
		Name:  "upgrade",
		Group: "Releases",
		Usage: []cmdUsage{{"<release> <chart>", "Upgrade a release to a new revision"}},
		Flags: []string{
			"--values",
			"--set",
			"--set-file",
			"--version",
			"--install",
			"--reuse-values",
			"--dry-run",
			"--wait",
			"--timeout",
			"--history-max",
			"--resolve-image",
			"--skip-compat-check",
			"--no-repo-update",
		},
		Run: chartsUpgrade,
	},
	{
		Name:  "uninstall",
		Group: "Releases",
		Usage: []cmdUsage{{"<release>", "Remove a release (keeps volumes)"}},
		Flags: []string{"--purge-volumes"},
		Run:   chartsUninstall,
	},
	{
		Name:  "rollback",
		Group: "Releases",
		Usage: []cmdUsage{{"<release> <rev>", "Re-deploy the contents of a past revision"}},
		Flags: []string{"--wait", "--timeout", "--history-max"},
		Run:   chartsRollback,
	},
	{
		Name:  "history",
		Group: "Releases",
		Usage: []cmdUsage{{"<release>", "Show a release's revision history"}},
		Run:   chartsHistory,
	},
	{
		Name:  "prune",
		Group: "Releases",
		Usage: []cmdUsage{{"[release]", "Delete old revisions beyond --history-max"}},
		Flags: []string{"--history-max", "--dry-run"},
		Run:   chartsPrune,
	},
	{
		Name:  "get",
		Group: "Releases",
		Usage: []cmdUsage{{"values|manifest <release>", "Show stored values or rendered manifest"}},
		Flags: []string{"--revision"},
		Run:   chartsGet,
	},
	{
		Name:  "diff",
		Group: "Releases",
		Usage: []cmdUsage{{"upgrade <release> <chart>", "Preview manifest changes before upgrading"}},
		Flags: []string{
			"--values",
			"--set",
			"--set-file",
			"--version",
			"--reuse-values",
			"--skip-compat-check",
			"--no-repo-update",
		},
		Run: chartsDiff,
	},
	{
		Name:    "list",
		Aliases: []string{"ls"},
		Group:   "Releases",
		Usage:   []cmdUsage{{"", "List releases"}},
		Run:     chartsList,
	},
	{
		Name:  "status",
		Group: "Releases",
		Usage: []cmdUsage{{"<release>", "Show release status and services"}},
		Run:   chartsStatus,
	},
	{
		Name:  "apply",
		Group: "GitOps",
		Usage: []cmdUsage{{"-f <file>", "Converge the swarm to a declarative release file"}},
		// apply refuses what it does not honour rather than ignoring it. For a
		// command whose entire contract is "the file is the only source of
		// truth", quietly discarding --set or --version would be a correctness
		// bug, not a cosmetic one.
		FlagHint: "the release file is the only source of truth (set chart versions and values there)",
		Flags: []string{
			"--values",
			"--dry-run",
			"--diff",
			"--wait",
			"--timeout",
			"--history-max",
			"--resolve-image",
			"--skip-compat-check",
			"--no-repo-update",
		},
		Run: chartsApply,
	},
	{
		Name:  "outdated",
		Group: "GitOps",
		Usage: []cmdUsage{{"", "Show releases with a newer chart version available"}},
		Flags: []string{"--no-repo-update"},
		Run:   chartsOutdated,
	},
}

// lookupCommand resolves a name or alias to its row.
func lookupCommand(name string) (chartsCmd, bool) {
	for _, c := range chartsCommands {
		if c.Name == name {
			return c, true
		}
		for _, a := range c.Aliases {
			if a == name {
				return c, true
			}
		}
	}
	return chartsCmd{}, false
}

// commandNames lists every name and alias, sorted so a suggestion breaks ties
// deterministically.
func commandNames() []string {
	var names []string
	for _, c := range chartsCommands {
		names = append(names, c.Name)
		names = append(names, c.Aliases...)
	}
	sort.Strings(names)
	return names
}

// suggestCommand returns the closest command name to input within an edit
// distance threshold, or "" if nothing is close enough. Same shape as the
// TUI's unknown-flag suggestion (commands/api.suggestFlag), including the
// threshold, so a typo is answered the same way on both surfaces.
func suggestCommand(input string, names []string) string {
	threshold := len(input) / 3
	if threshold < 2 {
		threshold = 2
	}
	best := ""
	bestDist := threshold + 1
	for _, n := range names {
		if d := registry.Distance(input, n); d < bestDist {
			best, bestDist = n, d
		}
	}
	if bestDist > threshold {
		return ""
	}
	return best
}

// renderCommands renders the grouped command list for the usage text.
func renderCommands() string {
	return renderList("", ":", "")
}

// renderCommandsShell renders the same list as the READMEs show it: commands as
// an operator types them, with the summary as a trailing comment, inside a
// shell fence.
func renderCommandsShell() string {
	return renderList("swarmcli charts ", "", "# ")
}

// renderList is the one renderer behind both. prefix starts each command line,
// groupSuffix follows a group heading, and comment starts each summary.
func renderList(prefix, groupSuffix, comment string) string {
	type line struct{ left, summary string }

	// One column width across every group, so the block reads as one table
	// rather than a stack of independently aligned ones.
	var lines []line
	width := 0
	for _, g := range chartGroups {
		for _, c := range chartsCommands {
			if c.Group != g {
				continue
			}
			for i, u := range c.Usage {
				left := strings.TrimRight(prefix+c.Name+" "+u.Args, " ")
				summary := u.Summary
				// The alias is reported on the command's first line rather than
				// given a line of its own: it is the same command, and a second
				// entry would read as a second thing to learn.
				if i == 0 && len(c.Aliases) > 0 {
					summary += " (alias: " + strings.Join(c.Aliases, ", ") + ")"
				}
				lines = append(lines, line{left, summary})
				if len(left) > width {
					width = len(left)
				}
			}
		}
	}

	var b strings.Builder
	i := 0
	for _, g := range chartGroups {
		count := 0
		for _, c := range chartsCommands {
			if c.Group == g {
				count += len(c.Usage)
			}
		}
		if count == 0 {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		heading := g + groupSuffix
		if comment != "" {
			heading = comment + heading
		}
		b.WriteString(heading + "\n")
		for ; count > 0; count-- {
			l := lines[i]
			i++
			indent := "  "
			if comment != "" {
				indent = "" // a shell block is copy-pasteable, so it is not indented
			}
			b.WriteString(indent + l.left + strings.Repeat(" ", width-len(l.left)+2) + comment + l.summary + "\n")
		}
	}
	return b.String()
}

// flagDoc describes one option: the value it takes, if any, and what it does.
// The order here is the order the option reference renders in.
type flagDoc struct {
	Name  string
	Short string
	Value string
	Desc  string
}

// flagDocs is the option reference. Every flag a row lists has an entry, and
// per-command help renders the subset that command accepts — so what an option
// does is written once, not once per command that takes it.
var flagDocs = []flagDoc{
	{Name: "--values", Short: "-f", Value: "<file>", Desc: "Values file (repeatable). For apply: the release file."},
	{Name: "--set", Value: "k=v", Desc: "Override a value (repeatable)"},
	{Name: "--set-file", Value: "k=path", Desc: "Set a value to a file's contents (repeatable). A chart references it as values/<k> from a config's file:, which is how an operator supplies a config the chart does not ship. See charts/README.md."},
	{Name: "--version", Value: "<ver>", Desc: "Chart version, for a <repo>/<chart> reference (default: latest). Not valid with a local chart path — its Chart.yaml sets the version."},
	{Name: "--dry-run", Desc: "Render and validate without deploying"},
	{Name: "--requirements", Desc: "Emit the rendered requirements.yaml, not the manifest"},
	{Name: "--wait", Desc: "Wait for services to converge"},
	{Name: "--timeout", Value: "<dur>", Desc: "Wait timeout, e.g. 10m (default 5m)"},
	{Name: "--history-max", Value: "<n>", Desc: "Max release revisions to retain"},
	{Name: "--resolve-image", Value: "<mode>", Desc: "always | changed | never — how the daemon resolves image tags to digests at deploy time (default: always)"},
	{Name: "--install", Desc: "Install the release if it is absent"},
	{Name: "--reuse-values", Desc: "Layer overrides on the previous release's values"},
	{Name: "--revision", Value: "<n>", Desc: "Select a specific revision"},
	{Name: "--purge-volumes", Desc: "Also remove the release's volumes (connected node only; every node is a Business Edition feature)"},
	{Name: "--diff", Desc: "Show each changed release's manifest diff (implies --dry-run)"},
	{Name: "--no-repo-update", Desc: "Resolve from the cached repository indexes and touch no network (also: SWARMCLI_CHARTS_NO_AUTO_UPDATE=1)"},
	{Name: "--skip-compat-check", Desc: "Proceed despite a chart's unmet swarmcliVersion constraint"},
	{Name: "--for-version", Value: "<ver>", Desc: "Check the chart's swarmcliVersion against <ver> instead of this build's chart engine"},
}

// renderOptions renders the option reference for the given flags, or all of
// them when names is nil (the top-level usage text).
func renderOptions(names []string) string {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}

	type row struct{ left, desc string }
	var rows []row
	width := 0
	for _, d := range flagDocs {
		if names != nil && !want[d.Name] {
			continue
		}
		left := "    " + d.Name
		if d.Short != "" {
			left = d.Short + ", " + d.Name
		}
		if d.Value != "" {
			left += " " + d.Value
		}
		rows = append(rows, row{left, d.Desc})
		if len(left) > width {
			width = len(left)
		}
	}

	var b strings.Builder
	for _, r := range rows {
		indent := width + 4
		for i, line := range wrapText(r.desc, 78-indent) {
			if i == 0 {
				b.WriteString("  " + r.left + strings.Repeat(" ", width-len(r.left)+2) + line + "\n")
				continue
			}
			b.WriteString(strings.Repeat(" ", indent) + line + "\n")
		}
	}
	return b.String()
}

// wrapText breaks text into lines of at most width columns, on spaces.
func wrapText(text string, width int) []string {
	if width < 20 {
		width = 20
	}
	var lines []string
	line := ""
	for _, w := range strings.Fields(text) {
		switch {
		case line == "":
			line = w
		case len(line)+1+len(w) <= width:
			line += " " + w
		default:
			lines = append(lines, line)
			line = w
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// commandHelp renders `swarmcli charts <command> --help`: what the command
// takes, and the options it honours — which is the list that decides whether an
// invocation is accepted, so an operator never has to infer it from a global
// reference that covers every command at once.
func commandHelp(c chartsCmd) string {
	var b strings.Builder
	b.WriteString("Usage:\n")
	for _, u := range c.Usage {
		b.WriteString(strings.TrimRight("  swarmcli charts "+c.Name+" "+u.Args, " ") + "\n")
	}
	if len(c.Aliases) > 0 {
		b.WriteString("\nAlias: " + strings.Join(c.Aliases, ", ") + "\n")
	}
	b.WriteString("\n")
	for _, u := range c.Usage {
		b.WriteString("  " + u.Summary + "\n")
	}
	if len(c.Flags) == 0 {
		b.WriteString("\nThis command takes no options.\n")
		return b.String()
	}
	b.WriteString("\nOptions:\n")
	b.WriteString(renderOptions(c.Flags))
	return b.String()
}

// chartsUsage is the full `swarmcli charts --help` text: the generated command
// list, then the prose that explains the parts a list cannot.
func chartsUsage() string {
	return "Usage: swarmcli charts <command> [options]\n\n" +
		renderCommands() +
		chartsUsageProse +
		"\nOptions:\n" + renderOptions(nil) +
		"\nNot every option applies to every command — `swarmcli charts <command> --help`\n" +
		"lists what that command takes, and anything else is rejected rather than\n" +
		"quietly ignored.\n" +
		chartsUsageTail
}
