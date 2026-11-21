package main

import (
	"os"
	"path/filepath"
)

// canonicalizeApidoc rewrites the enum arrays of the two files swag wrote:
// swagger.json and docs.go. It must run on the output directory before the
// Makefile moves the files into the tree.
func canonicalizeApidoc(outputDir string) error {
	dirs := []string{
		"./coderd",
		"./coderd/workspaceconnwatcher",
		"./codersdk",
		"./enterprise/coderd",
		"./enterprise/wsproxy/wsproxysdk",
	}
	sources, err := scanSources(dirs)
	if err != nil {
		return err
	}
	for _, name := range []string{"swagger.json", "docs.go"} {
		path := filepath.Join(outputDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out, err := canonicalizeEnumArrays(data, sources)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, out, 0o600); err != nil {
			return err
		}
	}
	return nil
}
