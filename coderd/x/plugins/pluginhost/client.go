package pluginhost

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"

	"golang.org/x/xerrors"
	"storj.io/drpc/drpcconn"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/x/plugins/pluginhost/proto"
	"github.com/coder/coder/v2/coderd/x/plugins/sandbox"
	"github.com/coder/coder/v2/codersdk/x/pluginsdk/pluginproto"
)

// Client drives one plugin host. Load and Invoke are serialized because a
// dRPC connection runs one RPC at a time.
type Client struct {
	logger slog.Logger
	conn   *drpcconn.Conn
	rpc    proto.DRPCPluginHostClient
	stop   func()

	// slot is held for the duration of an RPC.
	slot chan struct{}

	done    chan struct{}
	waitErr error

	closeOnce sync.Once
}

func newClient(logger slog.Logger, rwc io.ReadWriteCloser, stop func()) *Client {
	conn := drpcconn.NewWithOptions(rwc, drpcconn.Options{Manager: managerOptions()})
	return &Client{
		logger: logger,
		conn:   conn,
		rpc:    proto.NewDRPCPluginHostClient(conn),
		stop:   stop,
		slot:   make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
}

// exited records the result of the plugin host and must be called once.
func (c *Client) exited(err error) {
	c.waitErr = err
	close(c.done)
	_ = c.conn.Close()
}

// run runs fn while holding the RPC slot. Canceling a dRPC call that is in
// flight terminates the whole connection, so fn gets a context that is never
// canceled. If ctx ends first, run returns ctx.Err() and fn keeps the slot
// until the plugin host finishes, which the sandbox call timeout bounds;
// canceled then reports true.
func (c *Client) run(ctx context.Context, fn func(rpcCtx context.Context, canceled func() bool) error) error {
	select {
	case c.slot <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.exitErr()
	}
	if err := c.exitErr(); err != nil {
		<-c.slot
		return err
	}
	var canceled atomic.Bool
	errCh := make(chan error, 1)
	go func() {
		defer func() { <-c.slot }()
		errCh <- fn(context.WithoutCancel(ctx), canceled.Load)
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		canceled.Store(true)
		return ctx.Err()
	}
}

// Load compiles and instantiates wasm in the plugin host, replacing any
// module loaded before, and returns its manifest.
func (c *Client) Load(ctx context.Context, wasm []byte, limits sandbox.Limits) (*pluginproto.Manifest, error) {
	var manifest *pluginproto.Manifest
	err := c.run(ctx, func(rpcCtx context.Context, _ func() bool) error {
		res, err := c.rpc.Load(rpcCtx, &proto.LoadRequest{Wasm: wasm, Limits: limitsToProto(limits)})
		if err != nil {
			return xerrors.Errorf("load plugin: %w", err)
		}
		manifest = res.GetManifest()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return manifest, nil
}

// Invoke runs export with input in the loaded module. host answers the
// module's host calls with ctx; a nil host rejects them. Errors from the
// module, such as a trap or timeout, are returned as errors. After ctx is
// canceled, the remaining host calls of this invocation are rejected.
func (c *Client) Invoke(ctx context.Context, export string, input []byte, host sandbox.HostFunc) ([]byte, error) {
	var output []byte
	err := c.run(ctx, func(rpcCtx context.Context, canceled func() bool) error {
		var err error
		output, err = c.invokeStream(ctx, rpcCtx, export, input, host, canceled)
		return err
	})
	if err != nil {
		return nil, err
	}
	return output, nil
}

func (c *Client) invokeStream(ctx, rpcCtx context.Context, export string, input []byte, host sandbox.HostFunc, canceled func() bool) ([]byte, error) {
	stream, err := c.rpc.Invoke(rpcCtx)
	if err != nil {
		return nil, xerrors.Errorf("invoke %s: %w", export, err)
	}
	defer stream.Close()

	err = stream.Send(&proto.InvokeMessage{Msg: &proto.InvokeMessage_Start{Start: &proto.InvokeStart{
		Export: export,
		Input:  input,
	}}})
	if err != nil {
		return nil, xerrors.Errorf("invoke %s: send start: %w", export, err)
	}
	for {
		msg, err := stream.Recv()
		if err != nil {
			return nil, xerrors.Errorf("invoke %s: %w", export, err)
		}
		switch m := msg.GetMsg().(type) {
		case *proto.InvokeMessage_HostCall:
			var result *pluginproto.HostResult
			switch {
			case canceled():
				result = &pluginproto.HostResult{Error: "invocation canceled"}
			case host == nil:
				result = &pluginproto.HostResult{Error: "host calls are not available"}
			default:
				result = host(ctx, m.HostCall)
				if result == nil {
					result = &pluginproto.HostResult{}
				}
			}
			err := stream.Send(&proto.InvokeMessage{Msg: &proto.InvokeMessage_HostResult{HostResult: result}})
			if err != nil {
				return nil, xerrors.Errorf("invoke %s: send host result: %w", export, err)
			}
		case *proto.InvokeMessage_Done:
			if m.Done.GetError() != "" {
				return nil, xerrors.New(m.Done.GetError())
			}
			return m.Done.GetOutput(), nil
		default:
			return nil, xerrors.Errorf("invoke %s: unexpected message %T", export, m)
		}
	}
}

func (c *Client) exitErr() error {
	select {
	case <-c.done:
		if c.waitErr != nil {
			return xerrors.Errorf("plugin host exited: %w", c.waitErr)
		}
		return xerrors.New("plugin host exited")
	default:
		return nil
	}
}

// Close stops the plugin host and waits for it to exit.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.stop()
		_ = c.conn.Close()
	})
	<-c.done
	return nil
}

// Done is closed when the plugin host has exited.
func (c *Client) Done() <-chan struct{} {
	return c.done
}

// Wait blocks until the plugin host exits and returns its exit error.
func (c *Client) Wait() error {
	<-c.done
	return c.waitErr
}

// StartInProcess serves a plugin host in a goroutine of this process. It
// provides no process isolation and exists for tests.
func StartInProcess(ctx context.Context, logger slog.Logger) (*Client, error) {
	serverConn, clientConn := net.Pipe()
	ctx, cancel := context.WithCancel(ctx)
	c := newClient(logger, clientConn, cancel)
	go func() {
		err := Serve(ctx, serverConn, &slogWriter{logger: logger})
		cancel()
		_ = serverConn.Close()
		c.exited(err)
	}()
	return c, nil
}

// slogWriter writes module output to a logger, one entry per write.
type slogWriter struct {
	logger slog.Logger
}

func (w *slogWriter) Write(p []byte) (int, error) {
	w.logger.Info(context.Background(), "plugin wrote output", slog.F("output", string(p)))
	return len(p), nil
}
