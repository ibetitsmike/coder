// Package chatdmig runs the fork's chatd migrations. A chatd migration is a
// node named by UUID. It lists the nodes and upstream migrations that it
// requires, and runs only after all of them. See README.md.
package chatdmig

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/xerrors"
)

//go:embed routes.txt nodes override
var forkFS embed.FS

// Ledger names where an upstream migration runs.
type Ledger string

const (
	// LedgerCore runs the migration as upstream wrote it, with
	// golang-migrate, in the public schema.
	LedgerCore Ledger = "core"
	// LedgerChatd runs the migration as a node with chatd first on the
	// search path, so the objects it creates land in chatd.
	LedgerChatd Ledger = "chatd"
	// LedgerMixed is a migration that creates objects in both schemas. A
	// person writes override/<name>.up.sql, which runs as its node.
	LedgerMixed Ledger = "mixed"
)

type Source string

const (
	SourceFork     Source = "fork"
	SourceUpstream Source = "upstream"
)

// AdoptNodeID names the fork node that moves the Coder Agents objects into
// the chatd schema. Every routed upstream node runs after it.
var AdoptNodeID = uuid.MustParse("01a1b58c-24fa-7d7d-91d6-202388cf7cc8")

// upstreamNamespace derives stable node IDs from upstream migration names,
// so a renumbered upstream file keeps its node.
var upstreamNamespace = uuid.MustParse("49467d84-4b1a-4404-b125-28e63ca5a3d7")

func UpstreamNodeID(name string) uuid.UUID {
	return uuid.NewSHA1(upstreamNamespace, []byte(name))
}

const defaultSearchPath = "chatd, public"

// Phase says when a node runs.
type Phase string

const (
	// PhaseStartup nodes run before the server starts.
	PhaseStartup Phase = "startup"
	// PhaseBackground nodes run while the server serves, for backfills that
	// would hold up startup. A startup node cannot require one.
	PhaseBackground Phase = "background"
)

// Requirement is one parent of a node: another node, or a core version.
type Requirement struct {
	Ref         string
	Node        uuid.UUID
	CoreVersion uint
}

type Node struct {
	ID              uuid.UUID
	Name            string
	Source          Source
	UpstreamVersion uint
	SQL             []byte
	SearchPath      string
	Requires        []Requirement
	Checksum        []byte
	Phase           Phase
	// NoTransaction runs the one statement of the node outside a
	// transaction, for CREATE INDEX CONCURRENTLY.
	NoTransaction bool
	// Repeat runs the one statement of the node, each time in its own
	// transaction, until it changes no rows.
	Repeat bool
}

type Route struct {
	Version uint
	Name    string
	Ledger  Ledger
}

type Graph struct {
	SplitName    string
	SplitVersion uint
	Routes       []Route
	// Nodes is in topological order.
	Nodes []*Node

	byID     map[uuid.UUID]*Node
	upstream map[string][]uint
	files    map[uint]string
	hash     string
}

// Load reads the fork nodes and routes embedded in this package. upstream is
// the directory of numbered upstream migrations.
func Load(upstream fs.FS) (*Graph, error) {
	return LoadFS(upstream, forkFS)
}

var upstreamFile = regexp.MustCompile(`^(\d{6})_(.+)\.up\.sql$`)

// LoadFS reads routes.txt, nodes/*.up.sql and override/*.up.sql from fork,
// builds the graph and validates it.
func LoadFS(upstream, fork fs.FS) (*Graph, error) {
	g := &Graph{
		byID:     map[uuid.UUID]*Node{},
		upstream: map[string][]uint{},
		files:    map[uint]string{},
	}
	hash := sha256.New()

	ups, err := fs.Glob(upstream, "*.up.sql")
	if err != nil {
		return nil, xerrors.Errorf("list upstream migrations: %w", err)
	}
	for _, f := range ups {
		m := upstreamFile.FindStringSubmatch(f)
		if m == nil {
			continue
		}
		v, _ := strconv.ParseUint(m[1], 10, 32)
		g.upstream[m[2]] = append(g.upstream[m[2]], uint(v))
		g.files[uint(v)] = f
	}

	routes, err := fs.ReadFile(fork, "routes.txt")
	if err != nil {
		return nil, xerrors.Errorf("read routes.txt: %w", err)
	}
	_, _ = hash.Write(routes)
	if err := g.parseRoutes(routes); err != nil {
		return nil, xerrors.Errorf("routes.txt: %w", err)
	}

	var forkNodes []*Node
	nodeFiles, err := fs.Glob(fork, "nodes/*.up.sql")
	if err != nil {
		return nil, xerrors.Errorf("list nodes: %w", err)
	}
	for _, f := range nodeFiles {
		body, err := fs.ReadFile(fork, f)
		if err != nil {
			return nil, xerrors.Errorf("read %s: %w", f, err)
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00", f, body)
		n, err := parseForkNode(path.Base(f), body)
		if err != nil {
			return nil, xerrors.Errorf("%s: %w", f, err)
		}
		forkNodes = append(forkNodes, n)
	}

	overrides := map[string][]byte{}
	overrideFiles, err := fs.Glob(fork, "override/*.up.sql")
	if err != nil {
		return nil, xerrors.Errorf("list overrides: %w", err)
	}
	for _, f := range overrideFiles {
		body, err := fs.ReadFile(fork, f)
		if err != nil {
			return nil, xerrors.Errorf("read %s: %w", f, err)
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00", f, body)
		overrides[strings.TrimSuffix(path.Base(f), ".up.sql")] = body
	}
	g.hash = fmt.Sprintf("%x", hash.Sum(nil))

	nodes := forkNodes
	routed, err := g.routedNodes(upstream, overrides)
	if err != nil {
		return nil, err
	}
	nodes = append(nodes, routed...)
	for _, n := range nodes {
		if other, ok := g.byID[n.ID]; ok {
			return nil, xerrors.Errorf("%s and %s share id %s", other.Name, n.Name, n.ID)
		}
		g.byID[n.ID] = n
	}
	for _, n := range forkNodes {
		if err := g.resolve(n); err != nil {
			return nil, xerrors.Errorf("node %s: %w", n.Name, err)
		}
	}
	if err := g.validate(nodes); err != nil {
		return nil, err
	}
	return g, nil
}

// Hash changes when routes.txt, a node or an override changes.
func (g *Graph) Hash() string { return g.hash }

func (g *Graph) Node(id uuid.UUID) *Node { return g.byID[id] }

// Route returns the ledger of an upstream version. Versions up to the split
// ran in core before the fork existed.
func (g *Graph) Route(version uint) Ledger {
	for _, r := range g.Routes {
		if r.Version == version {
			return r.Ledger
		}
	}
	return LedgerCore
}

func (g *Graph) parseRoutes(b []byte) error {
	sc := bufio.NewScanner(bytes.NewReader(b))
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		f := strings.Fields(text)
		if f[0] == "split" {
			if len(f) != 2 || g.SplitName != "" {
				return xerrors.Errorf("line %d: want one line \"split <upstream name>\"", line)
			}
			v, err := g.latest(f[1])
			if err != nil {
				return xerrors.Errorf("line %d: %w", line, err)
			}
			g.SplitName, g.SplitVersion = f[1], v
			continue
		}
		if len(f) != 3 {
			return xerrors.Errorf("line %d: want \"<version> <name> <ledger>\"", line)
		}
		v, err := strconv.ParseUint(f[0], 10, 32)
		if err != nil {
			return xerrors.Errorf("line %d: version: %w", line, err)
		}
		led := Ledger(f[2])
		if led != LedgerCore && led != LedgerChatd && led != LedgerMixed {
			return xerrors.Errorf("line %d: unknown ledger %q", line, f[2])
		}
		g.Routes = append(g.Routes, Route{Version: uint(v), Name: f[1], Ledger: led})
	}
	if g.SplitName == "" {
		return xerrors.New("no split line")
	}
	sort.Slice(g.Routes, func(i, j int) bool { return g.Routes[i].Version < g.Routes[j].Version })
	return sc.Err()
}

func (g *Graph) latest(name string) (uint, error) {
	vs := g.upstream[name]
	if len(vs) == 0 {
		return 0, xerrors.Errorf("no upstream migration named %q", name)
	}
	latest := vs[0]
	for _, v := range vs[1:] {
		latest = max(latest, v)
	}
	return latest, nil
}

func (g *Graph) routedNodes(upstream fs.FS, overrides map[string][]byte) ([]*Node, error) {
	var nodes []*Node
	prev := AdoptNodeID
	for _, r := range g.Routes {
		if r.Ledger == LedgerCore {
			continue
		}
		file := g.files[r.Version]
		var body []byte
		var err error
		switch r.Ledger {
		case LedgerMixed:
			var ok bool
			body, ok = overrides[r.Name]
			if !ok {
				return nil, xerrors.Errorf("%06d_%s is mixed: write override/%s.up.sql", r.Version, r.Name, r.Name)
			}
		default:
			body, err = fs.ReadFile(upstream, file)
			if err != nil {
				return nil, xerrors.Errorf("read upstream %06d_%s: %w", r.Version, r.Name, err)
			}
		}
		id := UpstreamNodeID(r.Name)
		nodes = append(nodes, &Node{
			ID:              id,
			Name:            fmt.Sprintf("%06d_%s", r.Version, r.Name),
			Source:          SourceUpstream,
			UpstreamVersion: r.Version,
			SQL:             body,
			SearchPath:      defaultSearchPath,
			Phase:           PhaseStartup,
			Requires: []Requirement{
				{Ref: fmt.Sprintf("core %d", r.Version), CoreVersion: r.Version},
				{Ref: prev.String(), Node: prev},
			},
			Checksum: checksum(body),
		})
		prev = id
	}
	return nodes, nil
}

var (
	nodeFile   = regexp.MustCompile(`^([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})_([a-z0-9_]+)\.up\.sql$`)
	searchPath = regexp.MustCompile(`^[a-z_][a-z0-9_]*(\s*,\s*[a-z_][a-z0-9_]*)*$`)
)

func parseForkNode(file string, body []byte) (*Node, error) {
	m := nodeFile.FindStringSubmatch(file)
	if m == nil {
		return nil, xerrors.New("file name must be <uuid>_<name>.up.sql")
	}
	n := &Node{
		ID:         uuid.MustParse(m[1]),
		Name:       strings.TrimSuffix(file, ".up.sql"),
		Source:     SourceFork,
		SQL:        body,
		SearchPath: defaultSearchPath,
		Checksum:   checksum(body),
		Phase:      PhaseStartup,
	}
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		text := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(text, "--") {
			break
		}
		key, value, ok := strings.Cut(strings.TrimSpace(strings.TrimPrefix(text, "--")), ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "requires":
			n.Requires = append(n.Requires, Requirement{Ref: value})
		case "search_path":
			if !searchPath.MatchString(value) {
				return nil, xerrors.Errorf("bad search_path %q", value)
			}
			n.SearchPath = value
		case "phase":
			n.Phase = Phase(value)
			if n.Phase != PhaseStartup && n.Phase != PhaseBackground {
				return nil, xerrors.Errorf("phase %q: want startup or background", value)
			}
		case "transaction":
			if value != "none" {
				return nil, xerrors.Errorf("transaction %q: want none", value)
			}
			n.NoTransaction = true
		case "repeat":
			if value != "until-no-rows" {
				return nil, xerrors.Errorf("repeat %q: want until-no-rows", value)
			}
			n.Repeat = true
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if n.NoTransaction && n.Repeat {
		return nil, xerrors.New("repeat runs each batch in a transaction, so it cannot use transaction: none")
	}
	if (n.NoTransaction || n.Repeat) && statementCount(string(body)) != 1 {
		return nil, xerrors.New("transaction: none and repeat need exactly one statement")
	}
	return n, nil
}

// statementCount counts the statements of sql, ignoring semicolons in
// comments and quotes.
func statementCount(sql string) int {
	n := 0
	for _, s := range strings.Split(stripLiterals(sql), ";") {
		if strings.TrimSpace(s) != "" {
			n++
		}
	}
	return n
}

// resolve turns the refs of a fork node into nodes or core versions.
func (g *Graph) resolve(n *Node) error {
	for i, r := range n.Requires {
		if name, ok := strings.CutPrefix(r.Ref, "upstream:"); ok {
			v, err := g.latest(name)
			if err != nil {
				return err
			}
			if v > g.SplitVersion && g.Route(v) != LedgerCore {
				n.Requires[i].Node = UpstreamNodeID(name)
			} else {
				n.Requires[i].CoreVersion = v
			}
			continue
		}
		if len(r.Ref) < 36 {
			return xerrors.Errorf("requires %q: want upstream:<name> or <uuid>[_<name>]", r.Ref)
		}
		id, err := uuid.Parse(r.Ref[:36])
		if err != nil {
			return xerrors.Errorf("requires %q: %w", r.Ref, err)
		}
		n.Requires[i].Node = id
	}
	return nil
}

func (g *Graph) validate(nodes []*Node) error {
	if g.byID[AdoptNodeID] == nil {
		return xerrors.Errorf("no adopt node %s", AdoptNodeID)
	}
	for _, n := range nodes {
		for _, r := range n.Requires {
			if r.Node == uuid.Nil {
				continue
			}
			p := g.byID[r.Node]
			if p == nil {
				return xerrors.Errorf("%s requires unknown %s", n.Name, r.Ref)
			}
			if n.Phase == PhaseStartup && p.Phase == PhaseBackground {
				return xerrors.Errorf("%s runs at startup, so it cannot require background node %s", n.Name, p.Name)
			}
		}
	}

	routed := map[uint]Route{}
	for _, r := range g.Routes {
		if r.Version <= g.SplitVersion {
			return xerrors.Errorf("route %06d_%s: versions up to the split %06d run in core", r.Version, r.Name, g.SplitVersion)
		}
		if _, ok := routed[r.Version]; ok {
			return xerrors.Errorf("two routes for version %06d", r.Version)
		}
		routed[r.Version] = r
	}
	for v, f := range g.files {
		if v <= g.SplitVersion {
			continue
		}
		r, ok := routed[v]
		if !ok {
			return xerrors.Errorf("upstream %s has no route: run chatdmig route", f)
		}
		if want := fmt.Sprintf("%06d_%s.up.sql", r.Version, r.Name); want != f {
			return xerrors.Errorf("route %s does not match upstream %s: run chatdmig route", want, f)
		}
	}
	for _, r := range g.Routes {
		if _, ok := g.files[r.Version]; !ok {
			return xerrors.Errorf("route %06d_%s: no such upstream migration", r.Version, r.Name)
		}
	}

	order, cycle := topoSort(nodes)
	if cycle != nil {
		return xerrors.Errorf("cycle: %s", strings.Join(cycle, " -> "))
	}
	g.Nodes = order

	reach := map[uuid.UUID]bool{AdoptNodeID: true}
	for _, n := range order {
		for _, r := range n.Requires {
			if reach[r.Node] {
				reach[n.ID] = true
			}
		}
		if !reach[n.ID] {
			return xerrors.Errorf("%s must require the adopt node, directly or through a parent", n.Name)
		}
	}
	return nil
}

// topoSort orders nodes so that each node comes after its parents. Ties go
// to the lower upstream version, then the name.
func topoSort(nodes []*Node) ([]*Node, []string) {
	byID := map[uuid.UUID]*Node{}
	indeg := map[uuid.UUID]int{}
	children := map[uuid.UUID][]*Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	for _, n := range nodes {
		for _, r := range n.Requires {
			if r.Node == uuid.Nil {
				continue
			}
			indeg[n.ID]++
			children[r.Node] = append(children[r.Node], n)
		}
	}
	less := func(a, b *Node) bool {
		if a.UpstreamVersion != b.UpstreamVersion {
			return a.UpstreamVersion < b.UpstreamVersion
		}
		return a.Name < b.Name
	}
	var ready, order []*Node
	for _, n := range nodes {
		if indeg[n.ID] == 0 {
			ready = append(ready, n)
		}
	}
	for len(ready) > 0 {
		sort.Slice(ready, func(i, j int) bool { return less(ready[i], ready[j]) })
		n := ready[0]
		ready = ready[1:]
		order = append(order, n)
		for _, c := range children[n.ID] {
			indeg[c.ID]--
			if indeg[c.ID] == 0 {
				ready = append(ready, c)
			}
		}
	}
	if len(order) == len(nodes) {
		return order, nil
	}
	return nil, findCycle(nodes, byID, indeg)
}

func findCycle(nodes []*Node, byID map[uuid.UUID]*Node, indeg map[uuid.UUID]int) []string {
	state := map[uuid.UUID]int{}
	var stack []*Node
	var cycle []string
	var visit func(n *Node) bool
	visit = func(n *Node) bool {
		state[n.ID] = 1
		stack = append(stack, n)
		for _, r := range n.Requires {
			p := byID[r.Node]
			if p == nil {
				continue
			}
			if state[p.ID] == 1 {
				for i := len(stack) - 1; i >= 0; i-- {
					cycle = append([]string{stack[i].Name}, cycle...)
					if stack[i] == p {
						break
					}
				}
				cycle = append(cycle, p.Name)
				return true
			}
			if state[p.ID] == 0 && visit(p) {
				return true
			}
		}
		stack = stack[:len(stack)-1]
		state[n.ID] = 2
		return false
	}
	for _, n := range nodes {
		if indeg[n.ID] > 0 && state[n.ID] == 0 && visit(n) {
			return cycle
		}
	}
	return []string{"unknown"}
}

func checksum(b []byte) []byte {
	s := sha256.Sum256(b)
	return s[:]
}
