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

// Create writes a tar.gz archive of the given paths to w.
func Create(w io.Writer, paths SourcePaths) error {
	gw := gzip.NewWriter(w)
	tw := tar.NewWriter(gw)

	for _, p := range paths {
		if err := addPath(tw, p); err != nil {
			return ErrCreateArchive.Wrap(err, p)
		}
	}

	return closeWriters(tw, gw)
}

// closeWriters flushes the tar then gzip writers, reporting the first failure.
func closeWriters(tw *tar.Writer, gw *gzip.Writer) error {
	if err := tw.Close(); err != nil {
		return ErrCreateArchive.Wrap(err)
	}
	if err := gw.Close(); err != nil {
		return ErrCreateArchive.Wrap(err)
	}
	return nil
}

func addPath(tw *tar.Writer, root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return writeEntry(tw, path, info)
	})
}

// writeEntry writes a single filesystem entry (and its contents, for regular
// files) into tw.
func writeEntry(tw *tar.Writer, path string, info os.FileInfo) error {
	header, err := buildHeader(path, info)
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	return copyFileTo(tw, path)
}

// buildHeader constructs a tar header for path, resolving symlink targets.
func buildHeader(path string, info os.FileInfo) (*tar.Header, error) {
	link := ""
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return nil, err
		}
		link = target
	}

	header, err := tar.FileInfoHeader(info, link)
	if err != nil {
		return nil, err
	}
	header.Name = path
	return header, nil
}

// copyFileTo opens path and copies its contents into w.
func copyFileTo(w io.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return copyAndClose(w, f)
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

// Extract reads a tar.gz archive from r and extracts it into dest.
// Returns the list of extracted paths.
func Extract(r io.Reader, dest DestDir) ([]string, error) {
	return walkArchive(r, func(tr *tar.Reader, header *tar.Header) error {
		return extractEntry(string(dest), header, tr)
	})
}

// List reads a tar.gz archive from r and returns entry names.
func List(r io.Reader) ([]string, error) {
	return walkArchive(r, func(*tar.Reader, *tar.Header) error { return nil })
}

// entryFunc handles a single tar entry during a walk.
type entryFunc func(tr *tar.Reader, header *tar.Header) error

// walkArchive decompresses r and invokes handle for each tar entry, returning
// the collected entry names.
func walkArchive(r io.Reader, handle entryFunc) ([]string, error) {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return nil, ErrExtract.Wrap(err)
	}
	defer func() { _ = gr.Close() }()

	return walkEntries(tar.NewReader(gr), handle)
}

// walkEntries iterates tr, handling each entry and collecting its name.
func walkEntries(tr *tar.Reader, handle entryFunc) ([]string, error) {
	var names []string
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return names, nil
		}
		if err != nil {
			return nil, ErrExtract.Wrap(err)
		}
		if err := handle(tr, header); err != nil {
			return nil, err
		}
		names = append(names, header.Name)
	}
}

// extractEntry writes a single tar entry to disk under destDir, guarding
// against path traversal.
func extractEntry(destDir string, header *tar.Header, tr *tar.Reader) error {
	target := filepath.Join(destDir, header.Name)
	if !withinDir(target, destDir) {
		return ErrExtract.Wrap(nil, "path traversal: "+header.Name)
	}

	switch header.Typeflag {
	case tar.TypeDir:
		if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
			return ErrExtract.Wrap(err)
		}
	case tar.TypeReg:
		return extractFile(target, header, tr)
	}
	return nil
}

// withinDir reports whether target stays inside destDir.
func withinDir(target, destDir string) bool {
	clean := filepath.Clean(target)
	root := filepath.Clean(destDir)
	return clean == root || strings.HasPrefix(clean, root+string(os.PathSeparator))
}

func extractFile(target string, header *tar.Header, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return ErrExtract.Wrap(err)
	}

	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
	if err != nil {
		return ErrExtract.Wrap(err)
	}

	if err := copyAndClose(f, io.NopCloser(r)); err != nil {
		return ErrExtract.Wrap(err)
	}
	return nil
}
