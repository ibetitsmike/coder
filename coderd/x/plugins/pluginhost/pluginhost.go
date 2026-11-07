// Package pluginhost runs a plugin sandbox in a child process and exposes it
// to coderd over dRPC on the child's stdin and stdout.
package pluginhost

import (
	"context"
	"errors"
	"io"
	"math"
	"sync"
	"time"

	"golang.org/x/xerrors"
	"storj.io/drpc"
	"storj.io/drpc/drpcmanager"
	"storj.io/drpc/drpcmux"
	"storj.io/drpc/drpcserver"
	"storj.io/drpc/drpcwire"

	"cdr.dev/slog/v3"
	"cdr.dev/slog/v3/sloggers/sloghuman"
	"github.com/coder/coder/v2/coderd/x/plugins/pluginhost/proto"
	"github.com/coder/coder/v2/coderd/x/plugins/sandbox"
	"github.com/coder/coder/v2/codersdk/drpcsdk"
	"github.com/coder/coder/v2/codersdk/x/pluginsdk/pluginproto"
)

// MaxMessageSize bounds a single dRPC message between coderd and the plugin
// host. It must fit a whole Wasm module in a LoadRequest; Go plugins are
// commonly 10 MiB or more.
const MaxMessageSize = 64 << 20

func managerOptions() drpcmanager.Options {
	return drpcmanager.Options{
		Reader: drpcwire.ReaderOptions{MaximumBufferSize: MaxMessageSize},
	}
}

// Serve serves the PluginHost service on rwc until ctx is canceled or rwc is
// closed. Module stdout and stderr and server logs are written to logs.
func Serve(ctx context.Context, rwc io.ReadWriteCloser, logs io.Writer) error {
	if logs == nil {
		logs = io.Discard
	}
	srv := &server{logs: logs}
	defer srv.close()

	mux := drpcmux.New()
	if err := proto.DRPCRegisterPluginHost(mux, srv); err != nil {
		return xerrors.Errorf("register plugin host: %w", err)
	}
	logger := slog.Make(sloghuman.Sink(logs)).Named("pluginhost")
	d := drpcsdk.NewServer(logger, mux, drpcserver.Options{Manager: managerOptions()})

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = rwc.Close()
	}()
	err := d.ServeOne(ctx, rwc)
	if err != nil && ctx.Err() == nil && !isClosedError(err) {
		return xerrors.Errorf("serve plugin host: %w", err)
	}
	return nil
}

func isClosedError(err error) bool {
	return errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, context.Canceled) ||
		drpc.ClosedError.Has(err)
}

type server struct {
	logs io.Writer

	mu  sync.Mutex
	mod *sandbox.Module
}

func (s *server) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mod != nil {
		_ = s.mod.Close(context.Background())
		s.mod = nil
	}
}

func (s *server) Load(ctx context.Context, req *proto.LoadRequest) (*proto.LoadResponse, error) {
	mod, err := sandbox.New(ctx, req.GetWasm(), limitsFromProto(req.GetLimits()), s.logs)
	if err != nil {
		return nil, err
	}
	manifest, err := mod.Manifest(ctx)
	if err != nil {
		_ = mod.Close(ctx)
		return nil, err
	}
	s.mu.Lock()
	old := s.mod
	s.mod = mod
	s.mu.Unlock()
	if old != nil {
		_ = old.Close(ctx)
	}
	return &proto.LoadResponse{Manifest: manifest}, nil
}

func (s *server) Invoke(stream proto.DRPCPluginHost_InvokeStream) error {
	msg, err := stream.Recv()
	if err != nil {
		return err
	}
	start := msg.GetStart()
	if start == nil {
		return xerrors.New("first invoke message must be start")
	}
	s.mu.Lock()
	mod := s.mod
	s.mu.Unlock()
	if mod == nil {
		return sendDone(stream, nil, xerrors.New("no module is loaded"))
	}

	host := func(_ context.Context, call *pluginproto.HostCall) *pluginproto.HostResult {
		err := stream.Send(&proto.InvokeMessage{Msg: &proto.InvokeMessage_HostCall{HostCall: call}})
		if err != nil {
			return &pluginproto.HostResult{Error: "send host call: " + err.Error()}
		}
		reply, err := stream.Recv()
		if err != nil {
			return &pluginproto.HostResult{Error: "receive host result: " + err.Error()}
		}
		result := reply.GetHostResult()
		if result == nil {
			return &pluginproto.HostResult{Error: "expected a host result"}
		}
		return result
	}
	out, err := mod.Call(stream.Context(), start.GetExport(), start.GetInput(), host)
	return sendDone(stream, out, err)
}

func sendDone(stream proto.DRPCPluginHost_InvokeStream, output []byte, err error) error {
	done := &proto.InvokeDone{Output: output}
	if err != nil {
		done.Error = err.Error()
	}
	return stream.Send(&proto.InvokeMessage{Msg: &proto.InvokeMessage_Done{Done: done}})
}

func limitsToProto(l sandbox.Limits) *proto.Limits {
	return &proto.Limits{
		MemoryPages:    l.MemoryPages,
		CallTimeoutMs:  l.CallTimeout.Milliseconds(),
		MaxInputBytes:  clampUint32(l.MaxInputBytes),
		MaxOutputBytes: clampUint32(l.MaxOutputBytes),
		MaxLogBytes:    l.MaxLogBytes,
	}
}

func limitsFromProto(l *proto.Limits) sandbox.Limits {
	return sandbox.Limits{
		MemoryPages:    l.GetMemoryPages(),
		CallTimeout:    time.Duration(l.GetCallTimeoutMs()) * time.Millisecond,
		MaxInputBytes:  int(l.GetMaxInputBytes()),
		MaxOutputBytes: int(l.GetMaxOutputBytes()),
		MaxLogBytes:    l.GetMaxLogBytes(),
	}
}

func clampUint32(n int) uint32 {
	if n < 0 {
		return 0
	}
	if uint64(n) > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(n) //nolint:gosec // Bounded above.
}
