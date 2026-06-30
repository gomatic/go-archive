// Package archive creates, lists, and extracts tar.gz archives over streams.
// Create writes a gzip-compressed tar of filesystem paths to an io.Writer;
// Extract reads one from an io.Reader into a destination directory, guarding
// against path-traversal ("zip-slip"); List reports an archive's entry names
// without writing anything. Every failure carries a sentinel ([ErrCreateArchive]
// or [ErrExtract]) recoverable with errors.Is.
package archive

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// SourcePaths names the filesystem paths to archive.
type SourcePaths []string

// DestDir names the directory an archive is extracted into.
type DestDir string

// filePath names a single filesystem path (a source entry, a header name, or an
// extraction target).
type filePath string

// linkName names a symlink's target as recorded in (or extracted from) an entry.
type linkName string

// creator owns the tar+gzip writer chain a single Create call writes through.
// The stdlib writers are stateful machinery addressed by pointer, so they live
// in fields (where a pointer is idiomatic) rather than being threaded through
// every helper as parameters; the struct itself copies freely by value.
type creator struct {
	tw *tar.Writer
	gw *gzip.Writer
}

// Create writes a tar.gz archive of the given paths to w.
func Create(w io.Writer, paths SourcePaths) error {
	gw := gzip.NewWriter(w)
	c := creator{tw: tar.NewWriter(gw), gw: gw}

	for _, p := range paths {
		if err := c.addPath(filePath(p)); err != nil {
			return ErrCreateArchive.Wrap(err, p)
		}
	}

	return c.close()
}

// close flushes the tar then gzip writers, reporting the first failure.
func (c creator) close() error {
	if err := c.tw.Close(); err != nil {
		return ErrCreateArchive.Wrap(err)
	}
	if err := c.gw.Close(); err != nil {
		return ErrCreateArchive.Wrap(err)
	}
	return nil
}

// addPath walks root, writing every entry beneath it into the archive.
func (c creator) addPath(root filePath) error {
	return filepath.Walk(string(root), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return c.writeEntry(filePath(path), info)
	})
}

// writeEntry writes a single filesystem entry (and its contents, for regular
// files) into the archive.
func (c creator) writeEntry(path filePath, info os.FileInfo) error {
	header, err := buildHeader(path, info)
	if err != nil {
		return err
	}
	if err := c.tw.WriteHeader(&header); err != nil {
		return err
	}
	if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	return c.copyFile(path)
}

// copyFile opens path and copies its contents into the archive.
func (c creator) copyFile(path filePath) error {
	f, err := os.Open(string(path))
	if err != nil {
		return err
	}
	return copyAndClose(c.tw, f)
}

// buildHeader constructs a tar header for path, resolving symlink targets.
func buildHeader(path filePath, info os.FileInfo) (tar.Header, error) {
	link := ""
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(string(path))
		if err != nil {
			return tar.Header{}, err
		}
		link = target
	}

	header, err := tar.FileInfoHeader(info, link)
	if err != nil {
		return tar.Header{}, err
	}
	header.Name = string(path)
	return *header, nil
}

// copyAndClose copies all of r into w then closes r, reporting the copy error
// first and the close error otherwise.
func copyAndClose(w io.Writer, r io.ReadCloser) error {
	_, copyErr := io.Copy(w, r)
	closeErr := r.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// entryAction handles a single tar entry during a walk. The entry's body, when
// it has one, is read from the extractor's own reader.
type entryAction func(header tar.Header) error

// extractor owns the tar reader a single Extract/List call iterates. The reader
// is stateful stdlib machinery addressed by pointer, so it lives in a field
// rather than being passed to every helper.
type extractor struct {
	tr *tar.Reader
}

// Extract reads a tar.gz archive from r and extracts it into dest.
// Returns the list of extracted paths.
func Extract(r io.Reader, dest DestDir) ([]string, error) {
	e, done, err := newExtractor(r)
	if err != nil {
		return nil, err
	}
	defer done()

	return e.walk(func(header tar.Header) error {
		return extractEntry(dest, header, e.tr)
	})
}

// List reads a tar.gz archive from r and returns entry names.
func List(r io.Reader) ([]string, error) {
	e, done, err := newExtractor(r)
	if err != nil {
		return nil, err
	}
	defer done()

	return e.walk(noopEntry)
}

// noopEntry collects an entry's name without writing anything (List's action).
func noopEntry(tar.Header) error { return nil }

// newExtractor decompresses r and prepares an extractor over its tar stream,
// returning a cleanup that closes the gzip reader.
func newExtractor(r io.Reader) (extractor, func(), error) {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return extractor{}, nil, ErrExtract.Wrap(err)
	}
	return extractor{tr: tar.NewReader(gr)}, func() { _ = gr.Close() }, nil
}

// walk iterates the archive, invoking handle for each entry and collecting the
// entry names.
func (e extractor) walk(handle entryAction) ([]string, error) {
	var names []string
	for {
		header, err := e.tr.Next()
		if err == io.EOF {
			return names, nil
		}
		if err != nil {
			return nil, ErrExtract.Wrap(err)
		}
		if err := handle(*header); err != nil {
			return nil, err
		}
		names = append(names, header.Name)
	}
}

// extractEntry writes a single tar entry to disk under dest, guarding against
// path traversal. A regular file's body is read from r.
func extractEntry(dest DestDir, header tar.Header, r io.Reader) error {
	target := filePath(filepath.Join(string(dest), header.Name))
	if !withinDir(target, dest) {
		return ErrPathTraversal.Wrap(nil, header.Name)
	}

	switch header.Typeflag {
	case tar.TypeDir:
		if err := os.MkdirAll(string(target), os.FileMode(header.Mode)); err != nil {
			return ErrExtract.Wrap(err)
		}
	case tar.TypeReg:
		return extractFile(target, header, r)
	case tar.TypeSymlink:
		return extractSymlink(dest, target, linkName(header.Linkname))
	}
	return nil
}

// extractSymlink creates a symlink, rejecting one whose resolved target escapes
// dest (an absolute or ..-bearing link is a traversal vector).
func extractSymlink(dest DestDir, target filePath, link linkName) error {
	resolved := string(link)
	if !filepath.IsAbs(string(link)) {
		resolved = filepath.Join(filepath.Dir(string(target)), string(link))
	}
	if !withinDir(filePath(resolved), dest) {
		return ErrPathTraversal.Wrap(nil, "symlink target: "+string(link))
	}
	if err := os.Symlink(string(link), string(target)); err != nil {
		return ErrExtract.Wrap(err)
	}
	return nil
}

// withinDir reports whether target stays inside dest.
func withinDir(target filePath, dest DestDir) bool {
	clean := filepath.Clean(string(target))
	root := filepath.Clean(string(dest))
	return clean == root || strings.HasPrefix(clean, root+string(os.PathSeparator))
}

// extractWriter opens the destination for an extracted regular file. It is a
// seam so the close contract is assertable under test.
var extractWriter = osExtractWriter

func osExtractWriter(name filePath, perm os.FileMode) (io.WriteCloser, error) {
	return os.OpenFile(string(name), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
}

// extractFile writes header's body (read from r) to target, creating parent
// directories and always closing the destination.
func extractFile(target filePath, header tar.Header, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(string(target)), 0o755); err != nil {
		return ErrExtract.Wrap(err)
	}
	w, err := extractWriter(target, os.FileMode(header.Mode))
	if err != nil {
		return ErrExtract.Wrap(err)
	}
	// Copy then always close the destination — closing a no-op wrapper instead
	// would leak every extracted file's handle.
	_, copyErr := io.Copy(w, r)
	closeErr := w.Close()
	if copyErr != nil {
		return ErrExtract.Wrap(copyErr)
	}
	if closeErr != nil {
		return ErrExtract.Wrap(closeErr)
	}
	return nil
}
