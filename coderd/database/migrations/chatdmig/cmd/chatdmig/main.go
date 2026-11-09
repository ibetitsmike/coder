// Command chatdmig manages the fork's chatd migrations.
//
//	chatdmig route        route new upstream migrations; exit 3 if one is mixed,
//	                     1 if one is at or below a migration in early.txt
//	chatdmig new <name>   create a fork node; -requires adds a parent
//	chatdmig check        validate the graph and early.txt, print the order
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database/migrations/chatdmig"
)

const exitMixed = 3

var errMixed = xerrors.New("a mixed migration needs an override")

func main() {
	dir := flag.String("dir", "coderd/database/migrations", "the upstream migrations directory")
	flag.Usage = func() {
		_, _ = fmt.Fprintln(os.Stderr, "usage: chatdmig [-dir path] route | new <name> [-requires ref]... | check")
	}
	flag.Parse()
	var err error
	switch flag.Arg(0) {
	case "route":
		err = route(*dir)
	case "new":
		err = newNode(*dir, flag.Args()[1:])
	case "check":
		err = check(*dir)
	default:
		flag.Usage()
		os.Exit(2)
	}
	if errors.Is(err, errMixed) {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(exitMixed)
	}
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "chatdmig:", err)
		os.Exit(1)
	}
}

func load(dir string) (*chatdmig.Graph, error) {
	return chatdmig.LoadFS(os.DirFS(dir), os.DirFS(filepath.Join(dir, "chatdmig")))
}

func check(dir string) error {
	g, err := load(dir)
	if err != nil {
		return err
	}
	if _, err := loadEarly(dir, uint64(g.SplitVersion)); err != nil {
		return err
	}
	fmt.Printf("split %06d_%s, %d routes, %d nodes\n", g.SplitVersion, g.SplitName, len(g.Routes), len(g.Nodes))
	for _, n := range g.Nodes {
		var flags []string
		if n.Phase == chatdmig.PhaseBackground {
			flags = append(flags, "background")
		}
		if n.NoTransaction {
			flags = append(flags, "no transaction")
		}
		if n.Repeat {
			flags = append(flags, "repeat")
		}
		if len(flags) > 0 {
			fmt.Printf("  %s  %s  (%s)\n", n.ID, n.Name, strings.Join(flags, ", "))
		} else {
			fmt.Printf("  %s  %s\n", n.ID, n.Name)
		}
	}
	return nil
}

var upstreamFile = regexp.MustCompile(`^(\d{6})_(.+)\.up\.sql$`)

// route keeps the ledger of each known name, follows renumbered files and
// classifies each new upstream migration after the split.
func route(dir string) error {
	routesPath := filepath.Join(dir, "chatdmig", "routes.txt")
	raw, err := os.ReadFile(routesPath)
	if err != nil {
		return err
	}
	dump, err := os.ReadFile(filepath.Join(dir, "..", "dump.sql"))
	if err != nil {
		return err
	}
	classifier := chatdmig.NewClassifier(string(dump))

	var header []string
	var split string
	known := map[string]chatdmig.Ledger{}
	routedAt := map[string]uint64{}
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) == 0 || strings.HasPrefix(f[0], "#"):
			header = append(header, line)
		case f[0] == "split" && len(f) == 2:
			split = f[1]
			header = append(header, line)
		case len(f) == 3:
			v, err := strconv.ParseUint(f[0], 10, 32)
			if err != nil {
				return xerrors.Errorf("routes.txt: bad line %q", line)
			}
			known[f[1]] = chatdmig.Ledger(f[2])
			routedAt[f[1]] = v
		default:
			return xerrors.Errorf("routes.txt: bad line %q", line)
		}
	}

	files, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if err != nil {
		return err
	}
	sort.Strings(files)
	type entry struct {
		version uint64
		name    string
	}
	var entries []entry
	var splitVersion uint64
	for _, f := range files {
		m := upstreamFile.FindStringSubmatch(filepath.Base(f))
		if m == nil {
			continue
		}
		v, _ := strconv.ParseUint(m[1], 10, 32)
		if m[2] == split {
			splitVersion = max(splitVersion, v)
		}
		entries = append(entries, entry{v, m[2]})
	}
	if splitVersion == 0 {
		return xerrors.Errorf("split migration %q not found in %s", split, dir)
	}
	early, err := loadEarly(dir, splitVersion)
	if err != nil {
		return err
	}
	earlyNames := map[string]bool{}
	for _, x := range early {
		earlyNames[x.name] = true
	}

	out := header
	var mixed, skipped []string
	for _, e := range entries {
		if e.version <= splitVersion {
			continue
		}
		led, ok := known[e.name]
		if !ok {
			sql, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("%06d_%s.up.sql", e.version, e.name)))
			if err != nil {
				return err
			}
			led = classifier.Classify(string(sql))
			fmt.Printf("%06d_%s: %s\n", e.version, e.name, led)
		}
		// A migration routed before an early entry was added may sit below it,
		// so only new or renumbered migrations are checked. Databases that ran
		// one early migration ran every other one too.
		if !earlyNames[e.name] && routedAt[e.name] != e.version {
			for _, x := range early {
				if e.version <= x.version {
					skipped = append(skipped, fmt.Sprintf("upstream %06d_%s is at or below %06d_%s, which the fork took early from %s: a database that ran it would skip %06d_%s",
						e.version, e.name, x.version, x.name, x.source, e.version, e.name))
				}
			}
		}
		out = append(out, fmt.Sprintf("%06d %s %s", e.version, e.name, led))
		if led == chatdmig.LedgerMixed {
			if _, err := os.Stat(filepath.Join(dir, "chatdmig", "override", e.name+".up.sql")); err != nil {
				mixed = append(mixed, e.name)
			}
		}
	}
	if len(skipped) > 0 {
		return xerrors.Errorf("renumber or move the migration before syncing:\n%s", strings.Join(skipped, "\n"))
	}
	if err := os.WriteFile(routesPath, []byte(strings.Join(out, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	if len(mixed) > 0 {
		return xerrors.Errorf("%w: write %s", errMixed, "override/"+strings.Join(mixed, ".up.sql, override/")+".up.sql")
	}
	_, err = load(dir)
	return err
}

type earlyMigration struct {
	version uint64
	name    string
	source  string
}

// loadEarly parses early.txt, the upstream migrations that the fork took
// before upstream main merged them. Each must be above the split, because
// route ignores the migrations at or below it.
func loadEarly(dir string, splitVersion uint64) ([]earlyMigration, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "chatdmig", "early.txt"))
	if err != nil {
		return nil, xerrors.Errorf("read early.txt: %w", err)
	}
	var early []earlyMigration
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || strings.HasPrefix(f[0], "#") {
			continue
		}
		v, err := strconv.ParseUint(f[0], 10, 32)
		if len(f) != 3 || len(f[0]) != 6 || err != nil {
			return nil, xerrors.Errorf("early.txt: bad line %q", line)
		}
		if v <= splitVersion {
			return nil, xerrors.Errorf("early.txt: %06d_%s is at or below the split, which route does not check", v, f[1])
		}
		early = append(early, earlyMigration{v, f[1], f[2]})
	}
	return early, nil
}

func newNode(dir string, args []string) error {
	fs := flag.NewFlagSet("new", flag.ExitOnError)
	var requires []string
	fs.Func("requires", "a parent: upstream:<name> or <uuid>_<name>", func(s string) error {
		requires = append(requires, s)
		return nil
	})
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return xerrors.New("usage: chatdmig new <name> [-requires ref]...")
	}
	name := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if !regexp.MustCompile(`^[a-z0-9_]+$`).MatchString(name) {
		return xerrors.Errorf("name %q: use lower case, digits and _", name)
	}
	g, err := load(dir)
	if err != nil {
		return err
	}
	adopt := g.Node(chatdmig.AdoptNodeID)
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "-- requires: %s\n", adopt.Name)
	for _, r := range requires {
		_, _ = fmt.Fprintf(&b, "-- requires: %s\n", r)
	}
	b.WriteString("\n")
	path := filepath.Join(dir, "chatdmig", "nodes", fmt.Sprintf("%s_%s.up.sql", id, name))
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}
