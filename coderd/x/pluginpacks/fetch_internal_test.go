package pluginpacks

import (
	"bytes"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/stretchr/testify/require"
)

func TestCheckUnpackedSize(t *testing.T) {
	t.Parallel()

	// The second blob is stored as a delta of the first, so the pack is far
	// smaller than the 8209 bytes its objects inflate to.
	base := strings.Repeat("plugin pack ", 342)
	st := memory.NewStorage()
	var hashes []plumbing.Hash
	for _, content := range []string{base, base + "!"} {
		obj := st.NewEncodedObject()
		obj.SetType(plumbing.BlobObject)
		w, err := obj.Writer()
		require.NoError(t, err)
		_, err = w.Write([]byte(content))
		require.NoError(t, err)
		require.NoError(t, w.Close())
		h, err := st.SetEncodedObject(obj)
		require.NoError(t, err)
		hashes = append(hashes, h)
	}
	var pack bytes.Buffer
	_, err := packfile.NewEncoder(&pack, st, false).Encode(hashes, 10)
	require.NoError(t, err)
	require.Less(t, pack.Len(), 1000)
	require.True(t, hasDelta(t, pack.Bytes()), "pack must contain a delta")

	require.NoError(t, checkUnpackedSize(pack.Bytes(), 9000))
	// The base and the delta itself fit in 5000 bytes, but the object the
	// delta builds does not.
	require.ErrorContains(t, checkUnpackedSize(pack.Bytes(), 5000), "exceed 5000 bytes")
	require.ErrorContains(t, checkUnpackedSize(pack.Bytes(), 4000), "exceed 4000 bytes")
}

func hasDelta(t *testing.T, pack []byte) bool {
	t.Helper()
	s := packfile.NewScanner(bytes.NewReader(pack))
	_, count, err := s.Header()
	require.NoError(t, err)
	for range count {
		oh, err := s.NextObjectHeader()
		require.NoError(t, err)
		if oh.Type == plumbing.OFSDeltaObject || oh.Type == plumbing.REFDeltaObject {
			return true
		}
	}
	return false
}
