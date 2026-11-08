//go:build wasip1

package pluginsdk

import (
	"runtime"
	"unsafe"

	"golang.org/x/xerrors"
	"google.golang.org/protobuf/proto"

	"github.com/coder/coder/v2/codersdk/x/pluginsdk/pluginproto"
)

// The ABI passes byte buffers as 32-bit offsets into linear memory. A
// uint64 export result packs ptr<<32 | len.

//go:wasmimport coder call
func hostCall(ptr unsafe.Pointer, size uint32) uint32

//go:wasmimport coder result
func hostResult(ptr unsafe.Pointer)

var (
	// input is the buffer most recently returned by coder_alloc. The host
	// writes an export's input into it before calling the export.
	input []byte
	// output holds the bytes returned by the last export so they stay
	// reachable until the next export call.
	output []byte
)

func sendHostCall(call *pluginproto.HostCall) (*pluginproto.HostResult, error) {
	req, err := proto.Marshal(call)
	if err != nil {
		return nil, xerrors.Errorf("encode host call: %w", err)
	}
	size := hostCall(unsafe.Pointer(unsafe.SliceData(req)), uint32(len(req)))
	runtime.KeepAlive(req)
	buf := make([]byte, size)
	if size > 0 {
		hostResult(unsafe.Pointer(unsafe.SliceData(buf)))
	}
	var res pluginproto.HostResult
	if err := proto.Unmarshal(buf, &res); err != nil {
		return nil, xerrors.Errorf("decode host result: %w", err)
	}
	return &res, nil
}

func bufferPointer(b []byte) uint32 {
	if len(b) == 0 {
		return 0
	}
	return uint32(uintptr(unsafe.Pointer(unsafe.SliceData(b))))
}

func ret(b []byte) uint64 {
	output = b
	return uint64(bufferPointer(b))<<32 | uint64(len(b))
}

func readInput(ptr, size uint32) ([]byte, error) {
	if size == 0 {
		return nil, nil
	}
	if ptr != bufferPointer(input) || int(size) > len(input) {
		return nil, xerrors.New("input was not written to the coder_alloc buffer")
	}
	return input[:size], nil
}

//go:wasmexport coder_alloc
func coderAlloc(size uint32) uint32 {
	input = make([]byte, size)
	return bufferPointer(input)
}

//go:wasmexport coder_manifest
func coderManifest() uint64 {
	return ret(handleManifest(registered))
}

//go:wasmexport coder_on_tick
func coderOnTick() uint64 {
	return ret(handleTick(registered, sendHostCall))
}

//go:wasmexport coder_on_event
func coderOnEvent(ptr, size uint32) uint64 {
	in, err := readInput(ptr, size)
	if err != nil {
		return ret(mustMarshal(&pluginproto.Result{Error: err.Error()}))
	}
	return ret(handleEvent(registered, sendHostCall, in))
}

//go:wasmexport coder_on_http
func coderOnHTTP(ptr, size uint32) uint64 {
	in, err := readInput(ptr, size)
	if err != nil {
		return ret(mustMarshal(&pluginproto.HTTPResponse{Status: 400, Body: []byte(err.Error())}))
	}
	return ret(handleHTTP(registered, sendHostCall, in))
}

//go:wasmexport coder_on_connect
func coderOnConnect() uint64 {
	return ret(handleConnect(registered, sendHostCall))
}
