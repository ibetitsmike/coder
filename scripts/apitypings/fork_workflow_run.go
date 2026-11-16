package main

// This file is fork code: the TypeScript mutation of the fork-workflows
// experiment. main.go applies it from TSMutations with one hook line.

import (
	"fmt"

	"github.com/coder/guts"
	"github.com/coder/guts/bindings"
)

// WorkflowRunRawJSON maps the raw JSON fields of the workflow run detail and
// its steps to unknown instead of the global object type: main(args) accepts
// strings, lists, numbers and nested objects, and the generated
// Record<string, string> would force a cast on every consumer of a
// non-string argument or step report. A missing type or field fails the
// generation loudly, so a rename cannot silently skip the override.
// TestGeneration applies TSMutations to small testdata packages that do not
// carry the workflow run types, so the override stands down when the
// generated set holds no codersdk interface at all.
func WorkflowRunRawJSON(ts *guts.Typescript) {
	node, ok := ts.Node("WorkflowRunDetail")
	if !ok {
		if !hasCodersdkInterface(ts) {
			return
		}
		panic("WorkflowRunDetail was not generated")
	}
	unknown := bindings.KeywordUnknown
	iface, ok := node.(*bindings.Interface)
	if !ok {
		panic("WorkflowRunDetail is not an interface")
	}
	found := 0
	for _, field := range iface.Fields {
		switch field.Name {
		case "args", "result":
			field.Type = &unknown
			found++
		}
	}
	step, ok := ts.Node("WorkflowRunStep")
	if !ok {
		panic("WorkflowRunStep was not generated")
	}
	stepIface, ok := step.(*bindings.Interface)
	if !ok {
		panic("WorkflowRunStep is not an interface")
	}
	for _, field := range stepIface.Fields {
		if field.Name == "value" {
			field.Type = &unknown
			found++
		}
	}
	if found != 3 {
		panic(fmt.Sprintf("the workflow run types: found %d of the 3 raw JSON fields", found))
	}
}

// hasCodersdkInterface reports whether the generated set holds a codersdk
// interface. The testdata packages generate small type sets from files whose
// package path still says codersdk, so the path cannot tell them apart; the
// check is a count: a codersdk-sized output carries hundreds of interfaces,
// while a testdata set carries a handful. A codersdk-sized output without
// WorkflowRunDetail means the workflow types were renamed or dropped, and
// the override must fail loudly instead of silently skipping.
func hasCodersdkInterface(ts *guts.Typescript) bool {
	count := 0
	ts.ForEach(func(_ string, node bindings.Node) {
		if _, ok := node.(*bindings.Interface); ok {
			count++
		}
	})
	return count > 50
}
