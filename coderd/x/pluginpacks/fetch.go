// Package pluginpacks fetches plugin packs from Git repositories.
package pluginpacks

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/sideband"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
	"golang.org/x/xerrors"
)

// Limits of one fetch. The fetch fails when the repository exceeds one.
const (
	maxTransferBytes = 64 << 20
	maxFiles         = 2000
	maxFileBytes     = 1 << 20
	maxTotalBytes    = 16 << 20
)

var errTransferTooLarge = xerrors.Errorf("repository transfer exceeds %d bytes", maxTransferBytes)

// Source names a pack directory at a ref of a Git repository.
type Source struct {
	// URL is the https URL of the repository.
	URL string
	// Path is the slash-separated directory of the pack in the repository.
	// Empty is the repository root.
	Path string
	// Ref is a tag, a branch or a full commit SHA.
	Ref string
}

// File is a regular file of a pack.
type File struct {
	// Path is slash-separated and relative to the pack directory.
	Path    string
	Content []byte
}

// Snapshot is the content of a pack at one commit.
type Snapshot struct {
	CommitSHA string
	Files     []File
}

// Fetcher fetches packs over smart HTTP.
type Fetcher struct {
	// AllowedHosts are the host names that every request, redirects
	// included, must go to.
	AllowedHosts []string
	// Transport sends the requests. Nil means http.DefaultTransport.
	Transport http.RoundTripper
}

// Fetch resolves src.Ref, fetches only that commit and returns the regular
// files under src.Path. Symlinks and submodules are skipped.
func (f Fetcher) Fetch(ctx context.Context, src Source) (Snapshot, error) {
	u, err := url.Parse(src.URL)
	if err != nil {
		return Snapshot{}, xerrors.Errorf("parse source url: %w", err)
	}
	if err := f.checkURL(u); err != nil {
		return Snapshot{}, err
	}
	ep, err := transport.NewEndpoint(src.URL)
	if err != nil {
		return Snapshot{}, xerrors.Errorf("parse source url: %w", err)
	}

	next := f.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	rt := &guardedTransport{fetcher: f, next: next}
	rt.remaining.Store(maxTransferBytes)
	sess, err := githttp.NewClient(&http.Client{Transport: rt}).NewUploadPackSession(ep, nil)
	if err != nil {
		return Snapshot{}, xerrors.Errorf("open upload-pack session: %w", err)
	}
	defer sess.Close()

	adv, err := sess.AdvertisedReferencesContext(ctx)
	if err != nil {
		return Snapshot{}, xerrors.Errorf("list remote refs: %w", err)
	}
	want, err := resolveRef(adv, src.Ref)
	if err != nil {
		return Snapshot{}, err
	}

	req := packp.NewUploadPackRequestFromCapabilities(adv.Capabilities)
	req.Wants = []plumbing.Hash{want}
	req.Depth = packp.DepthCommits(1)
	if err := req.Capabilities.Set(capability.Shallow); err != nil {
		return Snapshot{}, xerrors.Errorf("request shallow fetch: %w", err)
	}
	resp, err := sess.UploadPack(ctx, req)
	if err != nil {
		return Snapshot{}, xerrors.Errorf("fetch commit %s: %w", want, err)
	}
	defer resp.Close()

	st := memory.NewStorage()
	if err := packfile.UpdateObjectStorage(st, demux(req.Capabilities, resp)); err != nil {
		return Snapshot{}, xerrors.Errorf("read packfile: %w", err)
	}
	commit, err := object.GetCommit(st, want)
	if err != nil {
		return Snapshot{}, xerrors.Errorf("read commit %s: %w", want, err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return Snapshot{}, xerrors.Errorf("read commit tree: %w", err)
	}
	if dir := strings.Trim(path.Clean("/"+src.Path), "/"); dir != "" {
		tree, err = tree.Tree(dir)
		if err != nil {
			return Snapshot{}, xerrors.Errorf("find pack directory %q: %w", dir, err)
		}
	}
	files, err := readFiles(tree)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{CommitSHA: want.String(), Files: files}, nil
}

func (f Fetcher) checkURL(u *url.URL) error {
	if u.Scheme != "https" {
		return xerrors.Errorf("source url %q must use https", u.Redacted())
	}
	if u.User != nil {
		return xerrors.New("source url must not contain credentials")
	}
	host := u.Hostname()
	if !slices.ContainsFunc(f.AllowedHosts, func(h string) bool { return strings.EqualFold(h, host) }) {
		return xerrors.Errorf("host %q is not an allowed plugin pack source", host)
	}
	return nil
}

func resolveRef(adv *packp.AdvRefs, ref string) (plumbing.Hash, error) {
	if ref == "" {
		return plumbing.ZeroHash, xerrors.New("ref is required")
	}
	if plumbing.IsHash(ref) {
		return plumbing.NewHash(ref), nil
	}
	for _, name := range []string{"refs/tags/" + ref, "refs/heads/" + ref} {
		if h, ok := adv.Peeled[name]; ok {
			return h, nil
		}
		if h, ok := adv.References[name]; ok {
			return h, nil
		}
	}
	return plumbing.ZeroHash, xerrors.Errorf("ref %q is not a tag, branch or full commit SHA of the repository", ref)
}

func demux(caps *capability.List, r io.Reader) io.Reader {
	switch {
	case caps.Supports(capability.Sideband64k):
		return sideband.NewDemuxer(sideband.Sideband64k, r)
	case caps.Supports(capability.Sideband):
		return sideband.NewDemuxer(sideband.Sideband, r)
	default:
		return r
	}
}

func readFiles(tree *object.Tree) ([]File, error) {
	var (
		files []File
		total int64
	)
	walker := object.NewTreeWalker(tree, true, nil)
	defer walker.Close()
	for {
		name, entry, err := walker.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return nil, xerrors.Errorf("walk pack tree: %w", err)
		}
		if entry.Mode != filemode.Regular && entry.Mode != filemode.Executable {
			continue
		}
		if len(files) == maxFiles {
			return nil, xerrors.Errorf("pack has more than %d files", maxFiles)
		}
		file, err := tree.TreeEntryFile(&entry)
		if err != nil {
			return nil, xerrors.Errorf("read %q: %w", name, err)
		}
		if file.Size > maxFileBytes {
			return nil, xerrors.Errorf("%q is larger than %d bytes", name, maxFileBytes)
		}
		total += file.Size
		if total > maxTotalBytes {
			return nil, xerrors.Errorf("pack is larger than %d bytes", maxTotalBytes)
		}
		content, err := readBlob(file)
		if err != nil {
			return nil, xerrors.Errorf("read %q: %w", name, err)
		}
		files = append(files, File{Path: name, Content: content})
	}
}

func readBlob(file *object.File) ([]byte, error) {
	r, err := file.Reader()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// guardedTransport checks the URL of every request, redirects included,
// and caps the bytes read from all response bodies of one fetch.
type guardedTransport struct {
	fetcher   Fetcher
	next      http.RoundTripper
	remaining atomic.Int64
}

func (t *guardedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.fetcher.checkURL(req.URL); err != nil {
		return nil, err
	}
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	resp.Body = &limitedBody{ReadCloser: resp.Body, remaining: &t.remaining}
	return resp, nil
}

type limitedBody struct {
	io.ReadCloser
	remaining *atomic.Int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if b.remaining.Add(-int64(n)) < 0 {
		return n, errTransferTooLarge
	}
	return n, err
}
