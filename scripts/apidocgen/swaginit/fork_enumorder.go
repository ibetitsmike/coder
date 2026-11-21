// This file canonicalizes the order of swag enum values for
// enum types whose constants swag collects from more than one file of the
// same package.
//
// swag v1.16.6 PackagesDefinitions.ParseTypes iterates a Go map of files
// when it collects package constants, so an enum type declared across two
// files of one package gets its enum values in a random order per run. In
// this fork that happens for every upstream enum a fork file extends, for
// example codersdk.Experiment (deployment.go plus fork_experiments.go).
//
// Canonical order: constants from files that are not named fork_*.go first
// in the order swag emitted them, then constants from fork_*.go files in
// scan order. Upstream only contributes constants from files that are not
// named fork_*.go, so an upstream only enum keeps whatever order swag
// produced, and generated files stay conflict free on syncs from upstream.
//
// Swag output does not carry the source file of each constant, but every
// fork file in this repository is named fork_*.go and upstream never names
// a file that way. The caller derives the fork marker per constant name
// from the scanned sources (see Plan) and this package only moves whole
// JSON array lines, so element bytes survive untouched.
package main

import (
	"bytes"

	"golang.org/x/xerrors"
)

// ErrShortArray is returned when an array has fewer elements than the
// permutation expects.
var ErrShortArray = xerrors.New("enum array length does not match x-enum-varnames")

// IsForkName reports whether a Go source file adds fork behavior. Every
// fork file in this repository is named fork_*.go, upstream never names a
// file that way.
func IsForkName(fileName string) bool {
	const forkPrefix = "fork_"
	base := fileName
	for i := len(fileName) - 1; i >= 0; i-- {
		if fileName[i] == '/' {
			base = fileName[i+1:]
			break
		}
	}
	return len(base) >= len(forkPrefix) && base[:len(forkPrefix)] == forkPrefix
}

// Plan returns the permutation that puts one enum's parallel arrays in
// canonical order, given the x-enum-varnames names, the fork marker for
// each name and the scan rank of each name. fork[i] is true when name i
// was declared in a fork_*.go file, rank[i] is the deterministic scan rank
// of the file that declared it (lower comes first) and is ignored for
// names that are not fork. It returns nil when nothing needs to move: no
// fork names, no non-fork names, fewer than two entries, or the input is
// already canonical. perm maps target index to source index, so
// out[i] = in[perm[i]].
//
// The input is already canonical exactly when every fork name sits after
// every non-fork name and the fork names are in rank order.
func Plan(names []string, fork []bool, rank []int) []int {
	if len(names) < 2 || len(fork) != len(names) || len(rank) != len(names) {
		return nil
	}
	var mainIdx, forkIdx []int
	for i := range names {
		if fork[i] {
			forkIdx = append(forkIdx, i)
		} else {
			mainIdx = append(mainIdx, i)
		}
	}
	if len(forkIdx) == 0 || len(mainIdx) == 0 {
		return nil
	}
	// Fork constants move to the back, sorted by scan rank so the order
	// does not depend on swag's random collection order.
	sortByRank(forkIdx, rank)
	perm := make([]int, 0, len(names))
	perm = append(perm, mainIdx...)
	perm = append(perm, forkIdx...)
	// perm[i] == i for every i only when the input is already canonical,
	// in which case nothing has to move.
	for i, src := range perm {
		if src != i {
			return perm
		}
	}
	return nil
}

// sortByRank sorts indices by rank, breaking ties by index, so the order
// is fully determined by the scan.
func sortByRank(idx []int, rank []int) {
	// Insertion sort: the fork blocks are tiny.
	for i := 1; i < len(idx); i++ {
		for j := i; j > 0; j-- {
			a, b := idx[j-1], idx[j]
			if rank[a] < rank[b] || (rank[a] == rank[b] && a < b) {
				break
			}
			idx[j-1], idx[j] = b, a
		}
	}
}

// ReorderArray rewrites one pretty printed JSON array in place: elems are
// the original element byte slices including each one's trailing comma
// except on the last, indent is the line indentation before each element,
// closeIndent is the indentation before the closing bracket. perm maps
// target index to source index. It returns the array text with each
// element's exact bytes preserved, swapping trailing commas so the
// element count and line count stay the same.
func ReorderArray(elems [][]byte, indent, closeIndent string, perm []int) ([]byte, error) {
	if len(elems) != len(perm) {
		return nil, ErrShortArray
	}
	var buf bytes.Buffer
	_ = buf.WriteByte('[')
	for _, src := range perm {
		_ = buf.WriteByte('\n')
		_, _ = buf.WriteString(indent)
		// splitArray keeps each element's trailing comma except on the
		// last; drop it here so separators are written uniformly.
		_, _ = buf.Write(bytes.TrimSuffix(elems[src], []byte(",")))
		_ = buf.WriteByte(',')
	}
	// Trim the trailing comma of the last element and close the array.
	out := buf.Bytes()
	out = out[:len(out)-1]
	out = append(out, '\n')
	out = append(out, closeIndent...)
	out = append(out, ']')
	return out, nil
}
