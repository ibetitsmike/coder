package main

import (
	"os"
	"testing"
)

// TestCanonicalizeRestoresRandomizedEnumOrder mutates the parallel enum
// arrays of one fork extended enum the way a swag run with a different file
// map order produces, then requires canonicalizeEnumArrays to restore the
// committed bytes exactly. Without the fork canonicalization hook the
// mutated input stays mutated, so the test fails.
func TestCanonicalizeRestoresRandomizedEnumOrder(t *testing.T) {
	t.Parallel()
	base := "../../../"
	dirs := []string{
		base + "coderd",
		base + "coderd/workspaceconnwatcher",
		base + "codersdk",
		base + "enterprise/coderd",
		base + "enterprise/wsproxy/wsproxysdk",
	}
	sources, err := scanSources(dirs)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"swagger.json", "docs.go"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(base + "coderd/apidoc/" + name)
			if err != nil {
				t.Fatal(err)
			}
			want := string(data)
			// The two files indent differently: tabs in swagger.json,
			// spaces in docs.go.
			ind := "\t\t\t\t"
			if name == "docs.go" {
				ind = "                "
			}
			// Mutate the enum and varnames arrays of
			// codersdk.ChatContextResourceKind together so the parallel
			// pairing stays intact. The swap moves the fork constant
			// subagent between two upstream constants, which the
			// canonicalizer must undo by putting it back at the end.
			needle := ind + "\"acp_harness\",\n" + ind + "\"subagent\"\n"
			repl := ind + "\"subagent\",\n" + ind + "\"acp_harness\"\n"
			vNeedle := ind + "\"ChatContextResourceKindACPHarness\",\n" + ind + "\"ChatContextResourceKindSubagent\"\n"
			vRepl := ind + "\"ChatContextResourceKindSubagent\",\n" + ind + "\"ChatContextResourceKindACPHarness\"\n"
			s, n1 := replaceOnce(want, needle, repl)
			if n1 != 1 {
				t.Fatalf("%s: enum needle found %d times", name, n1)
			}
			mutated, n2 := replaceOnce(s, vNeedle, vRepl)
			if n2 != 1 {
				t.Fatalf("%s: varnames needle found %d times", name, n2)
			}
			out, err := canonicalizeEnumArrays([]byte(mutated), sources)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if string(out) != want {
				for i := 0; i < len(out) && i < len(want); i++ {
					if out[i] != want[i] {
						lo := i - 60
						if lo < 0 {
							lo = 0
						}
						hi := i + 60
						if hi > len(want) {
							hi = len(want)
						}
						t.Fatalf("%s: first diff at %d\n got %q\nwant %q", name, i, out[lo:hi], want[lo:hi])
					}
				}
				t.Fatalf("%s: not restored (length got %d want %d)", name, len(out), len(want))
			}
		})
	}
}

// TestCanonicalizeIsIdempotentOnCommittedOutput requires the canonicalizer
// to leave the committed bytes untouched: the committed output is already
// canonical, and running gen again must not move anything.
func TestCanonicalizeIsIdempotentOnCommittedOutput(t *testing.T) {
	t.Parallel()
	base := "../../../"
	dirs := []string{
		base + "coderd",
		base + "coderd/workspaceconnwatcher",
		base + "codersdk",
		base + "enterprise/coderd",
		base + "enterprise/wsproxy/wsproxysdk",
	}
	sources, err := scanSources(dirs)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"swagger.json", "docs.go"} {
		data, err := os.ReadFile(base + "coderd/apidoc/" + name)
		if err != nil {
			t.Fatal(err)
		}
		out, err := canonicalizeEnumArrays(data, sources)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(out) != string(data) {
			t.Fatalf("%s: canonicalizer changed committed output", name)
		}
	}
}

// replaceOnce replaces the first occurrence of old with replacement and
// returns the result plus the number of occurrences of old in s.
func replaceOnce(s, old, replacement string) (string, int) {
	count := 0
	for i := 0; i+len(old) <= len(s); {
		if s[i:i+len(old)] == old {
			count++
			i += len(old)
			continue
		}
		i++
	}
	i := indexOfS(s, old)
	if i < 0 {
		return s, count
	}
	return s[:i] + replacement + s[i+len(old):], count
}

func indexOfS(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
