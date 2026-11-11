package reconnectingpty

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/pty"
	"github.com/coder/coder/v2/testutil"
)

func TestWithTerminalEnv(t *testing.T) {
	t.Parallel()

	defaultLocale := "C.UTF-8"
	if runtime.GOOS == "darwin" {
		defaultLocale = "UTF-8"
	}

	tests := []struct {
		name           string
		env            []string
		wantLCCTYPE    string
		wantLCCTYPESet bool
	}{
		{
			name:           "adds locale when missing",
			env:            []string{"PATH=/bin"},
			wantLCCTYPE:    defaultLocale,
			wantLCCTYPESet: true,
		},
		{
			name:           "adds locale when lang is not utf8",
			env:            []string{"LANG=C"},
			wantLCCTYPE:    defaultLocale,
			wantLCCTYPESet: true,
		},
		{
			name: "keeps utf8 lang",
			env:  []string{"LANG=C.UTF-8"},
		},
		{
			name: "keeps unhyphenated utf8 lang",
			env:  []string{"LANG=C.UTF8"},
		},
		{
			name:           "keeps utf8 ctype",
			env:            []string{"LC_CTYPE=C.UTF-8"},
			wantLCCTYPE:    "C.UTF-8",
			wantLCCTYPESet: true,
		},
		{
			name:           "overrides non utf8 ctype",
			env:            []string{"LANG=C.UTF-8", "LC_CTYPE=C"},
			wantLCCTYPE:    defaultLocale,
			wantLCCTYPESet: true,
		},
		{
			name: "keeps utf8 lc all",
			env:  []string{"LC_ALL=C.UTF-8"},
		},
		{
			name: "preserves non empty lc all",
			env:  []string{"LC_ALL=C"},
		},
		{
			name:           "ignores empty lc all",
			env:            []string{"LC_ALL="},
			wantLCCTYPE:    defaultLocale,
			wantLCCTYPESet: true,
		},
		{
			name: "continues after empty lc all",
			env:  []string{"LC_ALL=", "LANG=C.UTF-8"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := withTerminalEnv(tt.env)
			term, ok := envValue(got, "TERM")
			require.True(t, ok)
			require.Equal(t, xterm256Color, term)

			wantLCCTYPE := tt.wantLCCTYPE
			wantLCCTYPESet := tt.wantLCCTYPESet
			if runtime.GOOS == "windows" {
				wantLCCTYPE, wantLCCTYPESet = envValue(tt.env, "LC_CTYPE")
			}

			locale, ok := envValue(got, "LC_CTYPE")
			require.Equal(t, wantLCCTYPESet, ok)
			if wantLCCTYPESet {
				require.Equal(t, wantLCCTYPE, locale)
			}
		})
	}
}

// stubPTY records writes and resizes instead of touching a real terminal.
type stubPTY struct {
	mutex   sync.Mutex
	writes  []string
	resizes [][2]uint16
}

var _ pty.PTYCmd = (*stubPTY)(nil)

func (*stubPTY) Close() error             { return nil }
func (*stubPTY) OutputReader() io.Reader  { return strings.NewReader("") }
func (s *stubPTY) InputWriter() io.Writer { return (*stubPTYInput)(&s.writes) }

func (s *stubPTY) Resize(height uint16, width uint16) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.resizes = append(s.resizes, [2]uint16{height, width})
	return nil
}

type stubPTYInput []string

func (s *stubPTYInput) Write(p []byte) (int, error) {
	*s = append(*s, string(p))
	return len(p), nil
}

// runRequests drives readConnLoop over one JSON request per message and
// returns when the connection closes.
func runRequests(t *testing.T, ptty *stubPTY, reqs ...workspacesdk.ReconnectingPTYRequest) {
	t.Helper()

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		readConnLoop(context.Background(), server, ptty, prometheus.NewCounterVec(
			prometheus.CounterOpts{}, []string{"error_type"},
		), testutil.Logger(t))
	}()

	// net.Pipe is unbuffered, so the writer runs beside the read loop.
	go func() {
		for _, req := range reqs {
			data, err := json.Marshal(req)
			if err != nil {
				return
			}
			if _, err := client.Write(append(data, '\n')); err != nil {
				return
			}
		}
		_ = client.Close()
	}()

	select {
	case <-done:
	case <-time.After(testutil.WaitShort):
		t.Fatal("readConnLoop did not exit")
	}
}

func TestReconnectingPTYResizeOnlyDoesNotWrite(t *testing.T) {
	t.Parallel()

	ptty := &stubPTY{}
	runRequests(t, ptty, workspacesdk.ReconnectingPTYRequest{Height: 40, Width: 120})
	require.Empty(t, ptty.writes)
	require.Equal(t, [][2]uint16{{40, 120}}, ptty.resizes)
}

func TestReconnectingPTYDataAndResize(t *testing.T) {
	t.Parallel()

	t.Run("Both", func(t *testing.T) {
		t.Parallel()

		ptty := &stubPTY{}
		runRequests(t, ptty, workspacesdk.ReconnectingPTYRequest{Data: "echo test\r", Height: 40, Width: 120})
		require.Equal(t, []string{"echo test\r"}, ptty.writes)
		require.Equal(t, [][2]uint16{{40, 120}}, ptty.resizes)
	})

	t.Run("DataOnlyWritesExactlyThePayload", func(t *testing.T) {
		t.Parallel()

		ptty := &stubPTY{}
		runRequests(t, ptty, workspacesdk.ReconnectingPTYRequest{Data: "payload"})
		require.Equal(t, []string{"payload"}, ptty.writes)
		require.Empty(t, ptty.resizes)
	})
}

// BenchmarkResizeOnly measures a resize-only request that carries no data.
// A blocked zero-length write would take orders of magnitude longer than the
// 1ms budget, which is the failure mode the data guard removes.
func BenchmarkResizeOnly(b *testing.B) {
	ptty := &stubPTY{}
	req, err := json.Marshal(workspacesdk.ReconnectingPTYRequest{Height: 40, Width: 120})
	require.NoError(b, err)
	line := append(req, '\n')

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go func() {
		for i := 0; i < b.N; i++ {
			if _, err := client.Write(line); err != nil {
				return
			}
		}
		_ = client.Close()
	}()

	readConnLoop(context.Background(), server, ptty, prometheus.NewCounterVec(
		prometheus.CounterOpts{}, []string{"error_type"},
	), testutil.Logger(b))
}
