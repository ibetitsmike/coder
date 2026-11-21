package main

// This file is fork code: the TypeScript mutation of the fork-workflows
// experiment. main.go applies it from TSMutations with one hook line.

import (
	"fmt"

	"github.com/coder/guts"
	"github.com/coder/guts/bindings"
)

// WorkflowRunRawJSON maps the raw JSON fields of the workflow run detail,
// its steps and a saved workflow's args schema to unknown instead of the
// global object type: main(args) accepts strings, lists, numbers and
// nested objects, and an args schema is a JSON Schema object whose values
// are schemas, not strings. The generated Record<string, string> would
// force a cast on every consumer of a non-string value. A missing type or
// field fails the generation loudly, so a rename cannot silently skip the
// override. TestGeneration applies TSMutations to small testdata packages
// that do not carry the workflow types, so the override stands down when
// the generated set holds no codersdk interface at all.
func WorkflowRunRawJSON(ts *guts.Typescript) {
	node, ok := ts.Node("WorkflowRunDetail")
	if !ok {
		if !hasCodersdkInterface(ts) {
			return
		}
		panic("WorkflowRunDetail was not generated")
	}
	unknown := bindings.KeywordUnknown
	found := 0
	iface, ok := node.(*bindings.Interface)
	if !ok {
		panic("WorkflowRunDetail is not an interface")
	}
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
	// The list and single-workflow endpoints share the metadata type, and
	// the create and update requests repeat the field, so every
	// Record<string, string> args_schema leaves the generated set.
	schemaFields := 0
	for _, name := range []string{
		"ForkSavedWorkflowMetadata",
		"CreateForkSavedWorkflowRequest",
		"UpdateForkSavedWorkflowRequest",
	} {
		node, ok := ts.Node(name)
		if !ok {
			panic(fmt.Sprintf("%s was not generated", name))
		}
		requestIface, ok := node.(*bindings.Interface)
		if !ok {
			panic(fmt.Sprintf("%s is not an interface", name))
		}
		for _, field := range requestIface.Fields {
			if field.Name == "args_schema" {
				field.Type = &unknown
				schemaFields++
			}
		}
	}
	if found != 3 || schemaFields != 3 {
		panic(fmt.Sprintf("the workflow types: found %d of the 3 raw JSON run fields and %d of the 3 args schema fields", found, schemaFields))
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
