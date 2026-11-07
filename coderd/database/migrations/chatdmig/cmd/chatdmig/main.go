// Command chatdmig manages the fork's chatd migrations.
//
//	chatdmig route        route new upstream migrations; exit 3 if one is mixed
//	chatdmig new <name>   create a fork node; -requires adds a parent
//	chatdmig check        validate the graph and print its order
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
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) == 0 || strings.HasPrefix(f[0], "#"):
			header = append(header, line)
		case f[0] == "split" && len(f) == 2:
			split = f[1]
			header = append(header, line)
		case len(f) == 3:
			known[f[1]] = chatdmig.Ledger(f[2])
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

	out := header
	var mixed []string
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
		out = append(out, fmt.Sprintf("%06d %s %s", e.version, e.name, led))
		if led == chatdmig.LedgerMixed {
			if _, err := os.Stat(filepath.Join(dir, "chatdmig", "override", e.name+".up.sql")); err != nil {
				mixed = append(mixed, e.name)
			}
		}
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
