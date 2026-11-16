package agentcontext

// This file is fork code: it memoizes the file bodies a resolve pass reads,
// keyed by path and validated by the file's stat stamp, so a pass over an
// unchanged tree costs stat calls only. Every caller already holds a fresh
// stat of the file it reads, so a hit needs no extra syscall.

import (
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/xerrors"
)

const (
	// memoRacyWindow keeps a file whose mtime is this recent out of the
	// memo: a second write inside the filesystem's timestamp granularity
	// could keep its size and mtime, so its stamp would not change.
	memoRacyWindow = 2 * time.Second
	// memoIdleTTL drops entries no resolve pass used for this long, so a
	// removed or moved tree does not stay in memory.
	memoIdleTTL = 10 * time.Minute
	// memoSweepInterval bounds how often a pass sweeps the memo.
	memoSweepInterval = time.Minute
	// memoMaxBytes bounds the memory the memo retains, counted by
	// memoEntryCost. A body that would exceed it is read but not stored.
	memoMaxBytes = 64 << 20
	// memoEntryOverhead is what memoEntryCost charges for an entry beyond
	// its path and body, so many small or empty files still fill the
	// budget.
	memoEntryOverhead = 256
)

// memoEntryCost is what an entry for path with body data counts against
// memoMaxBytes.
func memoEntryCost(path string, data []byte) int64 {
	return int64(len(path) + len(data) + memoEntryOverhead)
}

// fileStamp identifies one version of a regular file. A rewrite changes the
// mtime or size, a replacement (rename over, symlink retarget) changes the
// inode, and a rewrite that restores the old mtime (cp -p, archive
// extraction) still changes the ctime.
type fileStamp struct {
	modTime int64
	size    int64
	mode    fs.FileMode
	id      fileID
}

// fileID is what the platform's stat says about a file beyond fs.FileInfo.
type fileID struct {
	dev   uint64
	ino   uint64
	ctime int64
}

// stampOf returns the stamp of a file, or false where the platform gives no
// file identity: without one, a replacement with the same size and mtime
// would keep the stamp, so such files are not memoized.
func stampOf(info fs.FileInfo) (fileStamp, bool) {
	id, ok := fileIdentity(info)
	if !ok {
		return fileStamp{}, false
	}
	return fileStamp{
		modTime: info.ModTime().UnixNano(),
		size:    info.Size(),
		mode:    info.Mode(),
		id:      id,
	}, true
}

type memoEntry struct {
	stamp    fileStamp
	data     []byte
	lastUsed time.Time
}

// fileMemo maps a path to the last body read from it.
type fileMemo struct {
	mu        sync.Mutex
	entries   map[string]*memoEntry
	bytes     int64
	lastSweep time.Time
}

var resolveMemo = &fileMemo{entries: make(map[string]*memoEntry)}

// read returns the body of the regular file at path whose stat is info,
// calling load only when the memo holds no body for the same stamp. load
// returns the body and the stat of the file it read. A body is stored only
// when load succeeded, read the file that info describes, and the file is
// not racily recent: a path replaced or retargeted between the stat and the
// open would otherwise store the new body under the old stamp. Callers must
// not modify the returned bytes: they are shared with the memo and with
// earlier snapshots.
func (m *fileMemo) read(path string, info fs.FileInfo, load func() ([]byte, fs.FileInfo, error)) ([]byte, error) {
	stamp, ok := stampOf(info)
	if !ok || !info.Mode().IsRegular() {
		data, _, err := load()
		return data, err
	}
	now := time.Now()
	m.mu.Lock()
	if e, ok := m.entries[path]; ok && e.stamp == stamp {
		e.lastUsed = now
		data := e.data
		m.mu.Unlock()
		return data, nil
	}
	m.mu.Unlock()

	data, opened, err := load()
	if err != nil {
		m.forget(path)
		return nil, err
	}
	if openedStamp, ok := stampOf(opened); !ok || openedStamp != stamp || now.Sub(info.ModTime()) < memoRacyWindow {
		m.forget(path)
		return data, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.entries[path]; ok {
		m.bytes -= memoEntryCost(path, old.data)
		delete(m.entries, path)
	}
	cost := memoEntryCost(path, data)
	if m.bytes+cost > memoMaxBytes {
		return data, nil
	}
	m.entries[path] = &memoEntry{stamp: stamp, data: data, lastUsed: now}
	m.bytes += cost
	return data, nil
}

func (m *fileMemo) forget(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.entries[path]; ok {
		m.bytes -= memoEntryCost(path, old.data)
		delete(m.entries, path)
	}
}

// sweep drops entries unused since before passStart minus memoIdleTTL. The
// Manager calls it after each resolve pass, so every entry that pass used is
// fresh and survives.
func (m *fileMemo) sweep(passStart time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if passStart.Sub(m.lastSweep) < memoSweepInterval {
		return
	}
	m.lastSweep = passStart
	cutoff := passStart.Add(-memoIdleTTL)
	for path, e := range m.entries {
		if e.lastUsed.Before(cutoff) {
			m.bytes -= memoEntryCost(path, e.data)
			delete(m.entries, path)
		}
	}
}

// readResourceFile reads the body of a resource file whose resolved stat is
// info, through the memo.
func readResourceFile(path string, info fs.FileInfo) ([]byte, error) {
	return resolveMemo.read(path, info, func() ([]byte, fs.FileInfo, error) {
		return readOpenFile(path, -1)
	})
}

// readOpenFile reads the file at path and returns its body and the stat of
// the descriptor it read. With maxBytes at 0 or more, a body longer than
// maxBytes is an error.
func readOpenFile(path string, maxBytes int64) ([]byte, fs.FileInfo, error) {
	noteBodyRead(path)
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	var r io.Reader = f
	if maxBytes >= 0 {
		r = io.LimitReader(f, maxBytes+1)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, nil, err
	}
	if maxBytes >= 0 && int64(len(data)) > maxBytes {
		return nil, nil, errFileGrew
	}
	return data, info, nil
}

// readCappedMemo reads the file at path when info, its stat, is a regular
// file of at most maxBytes. The read stays bounded even if the file grows
// after the stat.
func readCappedMemo(path string, info fs.FileInfo, maxBytes int64) ([]byte, bool) {
	if !info.Mode().IsRegular() || info.Size() > maxBytes {
		return nil, false
	}
	data, err := resolveMemo.read(path, info, func() ([]byte, fs.FileInfo, error) {
		return readOpenFile(path, maxBytes)
	})
	return data, err == nil
}

var errFileGrew = xerrors.New("file grew past its read cap")

// bodyReadObservers lets tests count the file bodies a pass reads. It is
// nil in production.
var bodyReadObservers atomic.Pointer[[]bodyReadObserver]

type bodyReadObserver struct {
	prefix string
	count  *atomic.Int64
}

// noteBodyRead reports a file body read to the observers whose directory
// holds path.
func noteBodyRead(path string) {
	observers := bodyReadObservers.Load()
	if observers == nil {
		return
	}
	for _, o := range *observers {
		if path == o.prefix || strings.HasPrefix(path, o.prefix+string(os.PathSeparator)) {
			o.count.Add(1)
		}
	}
}

var bodyReadObserversMu sync.Mutex

func addBodyReadObserver(o bodyReadObserver) {
	bodyReadObserversMu.Lock()
	defer bodyReadObserversMu.Unlock()
	var next []bodyReadObserver
	if cur := bodyReadObservers.Load(); cur != nil {
		next = slices.Clone(*cur)
	}
	next = append(next, o)
	bodyReadObservers.Store(&next)
}

func removeBodyReadObserver(count *atomic.Int64) {
	bodyReadObserversMu.Lock()
	defer bodyReadObserversMu.Unlock()
	cur := bodyReadObservers.Load()
	if cur == nil {
		return
	}
	next := slices.DeleteFunc(slices.Clone(*cur), func(o bodyReadObserver) bool { return o.count == count })
	if len(next) == 0 {
		bodyReadObservers.Store(nil)
		return
	}
	bodyReadObservers.Store(&next)
}
