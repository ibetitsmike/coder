// Package sandbox runs one plugin Wasm module in wazero with no access to
// the filesystem, environment, arguments or network of the host process.
package sandbox

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"golang.org/x/xerrors"
	"google.golang.org/protobuf/proto"

	"github.com/coder/coder/v2/codersdk/x/pluginsdk/pluginproto"
	"github.com/coder/quartz"
)

// Export names of the plugin ABI.
const (
	ExportAlloc    = "coder_alloc"
	ExportManifest = "coder_manifest"
	ExportOnEvent  = "coder_on_event"
	ExportOnTick   = "coder_on_tick"
	ExportOnHTTP   = "coder_on_http"

	exportInitialize = "_initialize"
	hostModuleName   = "coder"
)

// compilationCache shares compiled code between modules that are open at
// the same time. Entries are reference counted and freed when the last
// module using them closes. Every runtime must use the same
// WithCloseOnContextDone setting because the cache key does not include it.
var compilationCache = wazero.NewCompilationCache()

// errHostCallsUnavailable is returned to the guest for every host call made
// during a Call without a HostFunc.
const errHostCallsUnavailable = "host calls are not available"

// Limits bounds the resources a module may use.
type Limits struct {
	// MemoryPages caps linear memory in 64 KiB pages.
	MemoryPages uint32
	// CallTimeout bounds each Call, including host calls made during it.
	CallTimeout time.Duration
	// MaxInputBytes caps the input passed to an export.
	MaxInputBytes int
	// MaxOutputBytes caps the output of an export and the size of a single
	// HostCall sent by the guest.
	MaxOutputBytes int
	// MaxLogBytes caps the bytes the module may write to stdout and stderr
	// combined per minute. Excess output is dropped and a single truncation
	// marker is written per minute.
	MaxLogBytes int64
}

// DefaultLimits returns the limits used for zero fields of Limits.
func DefaultLimits() Limits {
	return Limits{
		MemoryPages:    2048,
		CallTimeout:    10 * time.Second,
		MaxInputBytes:  256 << 10,
		MaxOutputBytes: 1 << 20,
		MaxLogBytes:    256 << 10,
	}
}

func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.MemoryPages == 0 {
		l.MemoryPages = d.MemoryPages
	}
	if l.CallTimeout <= 0 {
		l.CallTimeout = d.CallTimeout
	}
	if l.MaxInputBytes <= 0 {
		l.MaxInputBytes = d.MaxInputBytes
	}
	if l.MaxOutputBytes <= 0 {
		l.MaxOutputBytes = d.MaxOutputBytes
	}
	if l.MaxLogBytes <= 0 {
		l.MaxLogBytes = d.MaxLogBytes
	}
	return l
}

// HostFunc answers one HostCall made by the guest. It runs on the goroutine
// executing the guest, so the guest is blocked until it returns.
type HostFunc func(ctx context.Context, call *pluginproto.HostCall) *pluginproto.HostResult

// Module is a compiled plugin and its current instance. Calls are serialized.
type Module struct {
	limits   Limits
	runtime  wazero.Runtime
	compiled wazero.CompiledModule
	config   wazero.ModuleConfig

	mu       sync.Mutex
	instance api.Module
	closed   bool
}

type callStateKey struct{}

type callState struct {
	host    HostFunc
	pending []byte
}

var requiredExports = map[string]struct {
	params  []api.ValueType
	results []api.ValueType
}{
	exportInitialize: {},
	ExportAlloc:      {params: []api.ValueType{api.ValueTypeI32}, results: []api.ValueType{api.ValueTypeI32}},
	ExportManifest:   {results: []api.ValueType{api.ValueTypeI64}},
	ExportOnTick:     {results: []api.ValueType{api.ValueTypeI64}},
	ExportOnEvent:    {params: []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}, results: []api.ValueType{api.ValueTypeI64}},
	ExportOnHTTP:     {params: []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}, results: []api.ValueType{api.ValueTypeI64}},
}

// New compiles wasm and instantiates it. Module stdout and stderr are written
// to logs, subject to Limits.MaxLogBytes.
func New(ctx context.Context, wasm []byte, limits Limits, logs io.Writer) (*Module, error) {
	limits = limits.withDefaults()
	if logs == nil {
		logs = io.Discard
	}
	runtime := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().
		WithMemoryLimitPages(limits.MemoryPages).
		WithCloseOnContextDone(true).
		WithCompilationCache(compilationCache))
	m := &Module{limits: limits, runtime: runtime}
	ok := false
	defer func() {
		if !ok {
			_ = m.release(ctx)
		}
	}()

	compiled, err := runtime.CompileModule(ctx, wasm)
	if err != nil {
		return nil, xerrors.Errorf("compile module: %w", err)
	}
	if err := validateExports(compiled); err != nil {
		return nil, err
	}
	for _, imp := range compiled.ImportedFunctions() {
		mod, name, _ := imp.Import()
		if mod != wasi_snapshot_preview1.ModuleName && mod != hostModuleName {
			return nil, xerrors.Errorf("module imports unsupported function %s.%s", mod, name)
		}
	}
	m.compiled = compiled

	// Go's wasip1 port needs WASI preview 1. Without a configured filesystem,
	// environment or arguments it exposes none of the host's, and it has no
	// socket dial functions.
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, runtime); err != nil {
		return nil, xerrors.Errorf("instantiate wasi: %w", err)
	}
	// The guest sends a HostCall with call, which returns the HostResult
	// length, then fetches the bytes with result into a buffer it allocated.
	// Two imports keep the host from calling back into the guest (for
	// coder_alloc) while an import is running, which Go guests do not allow.
	_, err = runtime.NewHostModuleBuilder(hostModuleName).
		NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(m.hostCall),
			[]api.ValueType{api.ValueTypeI32, api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32}).
		WithParameterNames("ptr", "len").
		Export("call").
		NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(hostResult),
			[]api.ValueType{api.ValueTypeI32}, nil).
		WithParameterNames("ptr").
		Export("result").
		Instantiate(ctx)
	if err != nil {
		return nil, xerrors.Errorf("instantiate host module: %w", err)
	}

	capped := newLogLimiter(logs, limits.MaxLogBytes, quartz.NewReal())
	m.config = wazero.NewModuleConfig().
		WithName("").
		WithStartFunctions(exportInitialize).
		WithStdout(capped).
		WithStderr(capped).
		WithSysWalltime().
		WithSysNanotime().
		WithRandSource(rand.Reader)

	initCtx, cancel := context.WithTimeout(ctx, limits.CallTimeout)
	defer cancel()
	m.instance, err = m.instantiate(initCtx)
	if err != nil {
		return nil, err
	}
	ok = true
	return m, nil
}

func validateExports(compiled wazero.CompiledModule) error {
	exports := compiled.ExportedFunctions()
	for name, want := range requiredExports {
		def, ok := exports[name]
		if !ok {
			return xerrors.Errorf("module does not export %s", name)
		}
		if !slices.Equal(def.ParamTypes(), want.params) || !slices.Equal(def.ResultTypes(), want.results) {
			return xerrors.Errorf("module export %s has an unexpected signature", name)
		}
	}
	if _, ok := compiled.ExportedMemories()["memory"]; !ok {
		return xerrors.New("module does not export memory")
	}
	return nil
}

func (m *Module) instantiate(ctx context.Context) (api.Module, error) {
	ctx = context.WithValue(ctx, callStateKey{}, &callState{})
	inst, err := m.runtime.InstantiateModule(ctx, m.compiled, m.config)
	if err != nil {
		return nil, xerrors.Errorf("instantiate module: %w", err)
	}
	return inst, nil
}

// Manifest calls the coder_manifest export.
func (m *Module) Manifest(ctx context.Context) (*pluginproto.Manifest, error) {
	out, err := m.Call(ctx, ExportManifest, nil, nil)
	if err != nil {
		return nil, err
	}
	var manifest pluginproto.Manifest
	if err := proto.Unmarshal(out, &manifest); err != nil {
		return nil, xerrors.Errorf("decode manifest: %w", err)
	}
	if manifest.Name == "" {
		return nil, xerrors.New("module returned a manifest without a name; plugins must call pluginsdk.Register from init")
	}
	return &manifest, nil
}

// Call runs export with input and returns its output. Exports that take no
// parameters require empty input. host answers the guest's host calls for the
// duration of this call; a nil host rejects them. If the guest traps or the
// call times out, the instance is discarded and the next Call starts a fresh
// one, losing guest state.
func (m *Module) Call(ctx context.Context, export string, input []byte, host HostFunc) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, xerrors.New("module is closed")
	}
	want, ok := requiredExports[export]
	if !ok || export == ExportAlloc || export == exportInitialize {
		return nil, xerrors.Errorf("unknown export %q", export)
	}
	if len(input) > m.limits.MaxInputBytes {
		return nil, xerrors.Errorf("input is %d bytes, more than the %d byte limit", len(input), m.limits.MaxInputBytes)
	}
	if len(want.params) == 0 && len(input) > 0 {
		return nil, xerrors.Errorf("export %s takes no input", export)
	}

	callCtx, cancel := context.WithTimeout(ctx, m.limits.CallTimeout)
	defer cancel()

	if m.instance == nil {
		inst, err := m.instantiate(callCtx)
		if err != nil {
			return nil, m.callError(ctx, export, err)
		}
		m.instance = inst
	}

	out, err := m.callExport(context.WithValue(callCtx, callStateKey{}, &callState{host: host}), export, input)
	if err != nil {
		var trap *guestError
		if errors.As(err, &trap) {
			_ = m.instance.Close(context.Background())
			m.instance = nil
			return nil, m.callError(ctx, export, trap.err)
		}
		return nil, err
	}
	return out, nil
}

// guestError marks errors after which the instance must not be reused.
type guestError struct{ err error }

func (e *guestError) Error() string { return e.err.Error() }

// callError describes err from a call made with a context derived from ctx
// with the CallTimeout deadline.
func (m *Module) callError(ctx context.Context, export string, err error) error {
	if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
		return xerrors.Errorf("call %s: timed out after %s: %w", export, m.limits.CallTimeout, err)
	}
	return xerrors.Errorf("call %s: %w", export, err)
}

func (m *Module) callExport(ctx context.Context, export string, input []byte) ([]byte, error) {
	inst := m.instance
	var params []uint64
	if len(requiredExports[export].params) > 0 {
		var ptr uint32
		if len(input) > 0 {
			res, err := inst.ExportedFunction(ExportAlloc).Call(ctx, uint64(len(input)))
			if err != nil {
				return nil, &guestError{err}
			}
			ptr = api.DecodeU32(res[0])
			if !inst.Memory().Write(ptr, input) {
				return nil, &guestError{xerrors.New("coder_alloc returned an out of range buffer")}
			}
		}
		params = []uint64{api.EncodeU32(ptr), api.EncodeU32(uint32(len(input)))} //nolint:gosec // Call caps input at MaxInputBytes.
	}
	res, err := inst.ExportedFunction(export).Call(ctx, params...)
	if err != nil {
		return nil, &guestError{err}
	}
	//nolint:gosec // The result packs two uint32 values.
	ptr, size := uint32(res[0]>>32), uint32(res[0])
	if int64(size) > int64(m.limits.MaxOutputBytes) {
		return nil, xerrors.Errorf("call %s: output is %d bytes, more than the %d byte limit", export, size, m.limits.MaxOutputBytes)
	}
	buf, ok := inst.Memory().Read(ptr, size)
	if !ok {
		return nil, &guestError{xerrors.New("export returned an out of range buffer")}
	}
	return append([]byte(nil), buf...), nil
}

func (m *Module) hostCall(ctx context.Context, mod api.Module, stack []uint64) {
	state, _ := ctx.Value(callStateKey{}).(*callState)
	if state == nil {
		state = &callState{}
	}
	ptr, size := api.DecodeU32(stack[0]), api.DecodeU32(stack[1])
	result := m.runHostCall(ctx, state.host, mod, ptr, size)
	out, err := proto.Marshal(result)
	if err != nil {
		out, _ = proto.Marshal(&pluginproto.HostResult{Error: "encode host result: " + err.Error()})
	}
	state.pending = out
	stack[0] = api.EncodeU32(uint32(len(out))) //nolint:gosec // Bounded by guest memory, which is at most 4 GiB.
}

func (m *Module) runHostCall(ctx context.Context, host HostFunc, mod api.Module, ptr, size uint32) *pluginproto.HostResult {
	if int64(size) > int64(m.limits.MaxOutputBytes) {
		return &pluginproto.HostResult{Error: "host call exceeds the output size limit"}
	}
	buf, ok := mod.Memory().Read(ptr, size)
	if !ok {
		return &pluginproto.HostResult{Error: "host call buffer is out of range"}
	}
	var call pluginproto.HostCall
	if err := proto.Unmarshal(append([]byte(nil), buf...), &call); err != nil {
		return &pluginproto.HostResult{Error: "decode host call: " + err.Error()}
	}
	if host == nil {
		return &pluginproto.HostResult{Error: errHostCallsUnavailable}
	}
	result := host(ctx, &call)
	if result == nil {
		return &pluginproto.HostResult{}
	}
	return result
}

func hostResult(ctx context.Context, mod api.Module, stack []uint64) {
	state, _ := ctx.Value(callStateKey{}).(*callState)
	if state == nil {
		return
	}
	pending := state.pending
	state.pending = nil
	if !mod.Memory().Write(api.DecodeU32(stack[0]), pending) {
		panic(xerrors.New("host result buffer is out of range"))
	}
}

// Close releases the runtime and its instance.
func (m *Module) Close(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	m.instance = nil
	return m.release(ctx)
}

func (m *Module) release(ctx context.Context) error {
	// Closing the runtime does not release entries in the shared
	// compilation cache; only closing the compiled module does.
	if m.compiled != nil {
		_ = m.compiled.Close(ctx)
	}
	return m.runtime.Close(ctx)
}
