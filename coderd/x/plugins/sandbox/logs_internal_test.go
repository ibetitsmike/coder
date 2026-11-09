package sandbox

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/quartz"
)

func TestLogLimiterWindow(t *testing.T) {
	t.Parallel()
	clock := quartz.NewMock(t)
	var buf bytes.Buffer
	l := newLogLimiter(&buf, 4, clock)

	n, err := l.Write([]byte("abcdef"))
	require.NoError(t, err)
	assert.Equal(t, 6, n)
	_, _ = l.Write([]byte("gh"))
	assert.Equal(t, "abcd"+string(logTruncatedMarker), buf.String())

	clock.Advance(time.Minute)
	buf.Reset()
	_, _ = l.Write([]byte("ij"))
	assert.Equal(t, "ij", buf.String())
	assert.False(t, strings.Contains(buf.String(), "truncated"))
}
