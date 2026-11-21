package main

import (
	"bytes"
	"testing"

	"golang.org/x/xerrors"
)

func TestIsForkName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		want bool
	}{
		{"fork_experiments.go", true},
		{"fork_agent_definitions.go", true},
		{"coderd/fork_chat_watch.go", true},
		{"deployment.go", false},
		{"chats.go", false},
		{"fork.go", false},
		{"", false},
		{"prefork.go", false},
	}
	for _, c := range cases {
		if got := IsForkName(c.name); got != c.want {
			t.Errorf("IsForkName(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPlan(t *testing.T) {
	t.Parallel()
	t.Run("nil when no fork names", func(t *testing.T) {
		t.Parallel()
		got := Plan([]string{"A", "B"}, []bool{false, false}, []int{0, 1})
		if got != nil {
			t.Errorf("Plan = %v, want nil", got)
		}
	})
	t.Run("nil when only fork names", func(t *testing.T) {
		t.Parallel()
		got := Plan([]string{"A", "B"}, []bool{true, true}, []int{0, 1})
		if got != nil {
			t.Errorf("Plan = %v, want nil", got)
		}
	})
	t.Run("nil when fewer than two entries", func(t *testing.T) {
		t.Parallel()
		if got := Plan([]string{"A"}, []bool{true}, []int{0}); got != nil {
			t.Errorf("Plan = %v, want nil", got)
		}
	})
	t.Run("nil when length mismatch", func(t *testing.T) {
		t.Parallel()
		if got := Plan([]string{"A", "B"}, []bool{true}, []int{0}); got != nil {
			t.Errorf("Plan = %v, want nil", got)
		}
	})
	t.Run("fork names move to the back ordered by rank", func(t *testing.T) {
		t.Parallel()
		got := Plan([]string{"M1", "M2", "F1", "F2", "M3"}, []bool{false, false, true, true, false}, []int{9, 8, 1, 0, 7})
		want := []int{0, 1, 4, 3, 2}
		if !equalInts(got, want) {
			t.Errorf("Plan = %v, want %v", got, want)
		}
	})
	t.Run("upstream first with rank ordered fork stays put", func(t *testing.T) {
		t.Parallel()
		got := Plan([]string{"M1", "M2", "F1", "F2"}, []bool{false, false, true, true}, []int{9, 8, 0, 1})
		if got != nil {
			t.Errorf("Plan = %v, want nil", got)
		}
	})
	t.Run("upstream first with fork out of rank order still moves", func(t *testing.T) {
		t.Parallel()
		got := Plan([]string{"M1", "M2", "F1", "F2"}, []bool{false, false, true, true}, []int{9, 8, 1, 0})
		want := []int{0, 1, 3, 2}
		if !equalInts(got, want) {
			t.Errorf("Plan = %v, want %v", got, want)
		}
	})
	t.Run("fork first is flipped back", func(t *testing.T) {
		t.Parallel()
		// A swag run that emitted fork constants before upstream constants
		// must be flipped so upstream keeps its own order first.
		got := Plan([]string{"F1", "F2", "M1", "M2"}, []bool{true, true, false, false}, []int{1, 0, 9, 8})
		want := []int{2, 3, 1, 0}
		if !equalInts(got, want) {
			t.Errorf("Plan = %v, want %v", got, want)
		}
	})
	t.Run("interleaved fork and main entries regroup stably", func(t *testing.T) {
		t.Parallel()
		got := Plan([]string{"M1", "F1", "M2", "F2"}, []bool{false, true, false, true}, []int{9, 0, 8, 1})
		want := []int{0, 2, 1, 3}
		if !equalInts(got, want) {
			t.Errorf("Plan = %v, want %v", got, want)
		}
	})
	t.Run("rank ties break by index", func(t *testing.T) {
		t.Parallel()
		got := Plan([]string{"F1", "F2", "M1"}, []bool{true, true, false}, []int{5, 5, 0})
		want := []int{2, 0, 1}
		if !equalInts(got, want) {
			t.Errorf("Plan = %v, want %v", got, want)
		}
	})
}

func TestReorderArray(t *testing.T) {
	t.Parallel()
	t.Run("identity round trip", func(t *testing.T) {
		t.Parallel()
		in := []byte("[\n\t\t\t\"a\",\n\t\t\t\"b\",\n\t\t\t\"c\"\n\t\t]")
		got, err := ReorderArray([][]byte{[]byte("\"a\""), []byte("\"b\""), []byte("\"c\"")}, "\t\t\t", "\t\t", []int{0, 1, 2})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, in) {
			t.Errorf("identity reorder changed bytes:\n got %q\nwant %q", got, in)
		}
	})
	t.Run("swap two elements", func(t *testing.T) {
		t.Parallel()
		got, err := ReorderArray([][]byte{[]byte("\"a\""), []byte("\"b\"")}, "\t\t\t", "\t\t", []int{1, 0})
		if err != nil {
			t.Fatal(err)
		}
		want := []byte("[\n\t\t\t\"b\",\n\t\t\t\"a\"\n\t\t]")
		if !bytes.Equal(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("preserves element bytes including escapes", func(t *testing.T) {
		t.Parallel()
		got, err := ReorderArray([][]byte{[]byte("\"x\\ny\""), []byte("\"z\"")}, "  ", "", []int{1, 0})
		if err != nil {
			t.Fatal(err)
		}
		want := []byte("[\n  \"z\",\n  \"x\\ny\"\n]")
		if !bytes.Equal(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("short array returns ErrShortArray", func(t *testing.T) {
		t.Parallel()
		_, err := ReorderArray([][]byte{[]byte("\"a\"")}, "\t", "\t", []int{0, 1})
		if !xerrors.Is(err, ErrShortArray) {
			t.Errorf("err = %v, want ErrShortArray", err)
		}
	})
	t.Run("long perm returns ErrShortArray", func(t *testing.T) {
		t.Parallel()
		_, err := ReorderArray([][]byte{[]byte("\"a\""), []byte("\"b\"")}, "\t", "\t", []int{0})
		if !xerrors.Is(err, ErrShortArray) {
			t.Errorf("err = %v, want ErrShortArray", err)
		}
	})
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
