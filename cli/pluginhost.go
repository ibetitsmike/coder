//go:build !slim

package cli

import (
	"io"

	"github.com/coder/coder/v2/coderd/x/plugins/pluginhost"
	"github.com/coder/serpent"
)

// pluginHost is started by coderd with a minimal environment and speaks dRPC
// on stdin and stdout, so it must not read configuration or write anything
// else to stdout.
func (*RootCmd) pluginHost() *serpent.Command {
	return &serpent.Command{
		Use:    "plugin-host",
		Short:  "Run a sandboxed plugin host on stdin and stdout. Started by coderd.",
		Hidden: true,
		Handler: func(inv *serpent.Invocation) error {
			return pluginhost.Serve(inv.Context(), &stdioConn{Reader: inv.Stdin, Writer: inv.Stdout}, inv.Stderr)
		},
	}
}

type stdioConn struct {
	io.Reader
	io.Writer
}

func (c *stdioConn) Close() error {
	if closer, ok := c.Reader.(io.Closer); ok {
		_ = closer.Close()
	}
	if closer, ok := c.Writer.(io.Closer); ok {
		_ = closer.Close()
	}
	return nil
}
