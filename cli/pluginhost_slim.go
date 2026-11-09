//go:build slim

package cli

import "github.com/coder/serpent"

func (*RootCmd) pluginHost() *serpent.Command {
	return &serpent.Command{
		Use:    "plugin-host",
		Short:  "Run a sandboxed plugin host on stdin and stdout. Started by coderd.",
		Hidden: true,
		Handler: func(inv *serpent.Invocation) error {
			SlimUnsupported(inv.Stderr, "plugin-host")
			return nil
		},
	}
}
