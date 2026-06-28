package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tarEntry describes a single entry to write into a synthetic tar.gz.
type tarEntry struct {
	name     string
	body     string
	mode     int64
	typeflag byte
}

// buildTarGz produces a gzip-compressed tar stream from the given entries,
// letting tests drive Extract/List down branches that os.Create cannot reach.
func buildTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Typeflag: e.typeflag, Mode: e.mode, Size: int64(len(e.body))}
		require.NoError(t, tw.WriteHeader(hdr))
		if e.body != "" {
			_, err := tw.Write([]byte(e.body))
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	return buf.Bytes()
}

// failWriter fails every write.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errBoom }

var errBoom = errString("boom")

type errString string

func (e errString) Error() string { return string(e) }

// failReadCloser fails on Read.
type failReadCloser struct{}

func (failReadCloser) Read([]byte) (int, error) { return 0, errBoom }
func (failReadCloser) Close() error             { return nil }

// failCloser reads cleanly to EOF but fails on Close.
type failCloser struct{ r *bytes.Reader }

func (c *failCloser) Read(p []byte) (int, error) { return c.r.Read(p) }
func (*failCloser) Close() error                 { return errBoom }

func TestCopyAndClose(t *testing.T) {
	t.Parallel()

	t.Run("copies then closes", func(t *testing.T) {
		t.Parallel()
		want, must := assert.New(t), require.New(t)

		var buf bytes.Buffer
		src := io.NopCloser(bytes.NewReader([]byte("payload")))
		must.NoError(copyAndClose(&buf, src))
		want.Equal("payload", buf.String())
	})

	t.Run("reports copy error before close", func(t *testing.T) {
		t.Parallel()
		require.ErrorIs(t, copyAndClose(&bytes.Buffer{}, failReadCloser{}), errBoom)
	})

	t.Run("reports close error when copy succeeds", func(t *testing.T) {
		t.Parallel()
		require.ErrorIs(t, copyAndClose(&bytes.Buffer{}, &failCloser{r: bytes.NewReader([]byte("ok"))}), errBoom)
	})
}

// budgetWriter accepts up to budget bytes, then fails every subsequent write.
type budgetWriter struct {
	budget int
	used   int
}

func (w *budgetWriter) Write(p []byte) (int, error) {
	if w.used+len(p) > w.budget {
		return 0, errBoom
	}
	w.used += len(p)
	return len(p), nil
}

func TestCloseWriters_TarCloseError(t *testing.T) {
	t.Parallel()
	must := require.New(t)

	// A zero-budget writer rejects the very first flush, which happens during
	// tw.Close (the gzip header), exercising the tar-close error branch.
	gw := gzip.NewWriter(&budgetWriter{budget: 0})
	tw := tar.NewWriter(gw)

	must.ErrorIs(closeWriters(tw, gw), ErrCreateArchive)
}

func TestCloseWriters_GzipCloseError(t *testing.T) {
	t.Parallel()
	must := require.New(t)

	// A budget of 10 bytes admits the gzip header written during tw.Close but
	// rejects the deflate body and trailer flushed by gw.Close.
	gw := gzip.NewWriter(&budgetWriter{budget: 10})
	tw := tar.NewWriter(gw)

	must.NoError(tw.Close())
	must.ErrorIs(closeWriters(tw, gw), ErrCreateArchive)
}

// fakeInfo is a synthetic os.FileInfo letting tests drive buildHeader down its
// symlink and unsupported-mode branches.
type fakeInfo struct {
	name string
	mode os.FileMode
}

func (f fakeInfo) Name() string      { return f.name }
func (fakeInfo) Size() int64         { return 0 }
func (f fakeInfo) Mode() os.FileMode { return f.mode }
func (fakeInfo) ModTime() time.Time  { return time.Time{} }
func (f fakeInfo) IsDir() bool       { return f.mode.IsDir() }
func (fakeInfo) Sys() any            { return nil }

func TestBuildHeader_ReadlinkError(t *testing.T) {
	t.Parallel()
	must := require.New(t)

	// Mode says symlink, but the path is a regular file, so os.Readlink fails.
	file := filepath.Join(t.TempDir(), "not-a-link")
	must.NoError(os.WriteFile(file, []byte("x"), 0o644))

	_, err := buildHeader(file, fakeInfo{name: "not-a-link", mode: os.ModeSymlink})
	must.Error(err)
}

func TestBuildHeader_FileInfoHeaderError(t *testing.T) {
	t.Parallel()
	must := require.New(t)

	// A socket mode cannot be represented in a tar header.
	_, err := buildHeader("sock", fakeInfo{name: "sock", mode: os.ModeSocket})
	must.Error(err)
}

func TestWriteEntry_BuildHeaderError(t *testing.T) {
	t.Parallel()
	must := require.New(t)

	// A socket mode makes buildHeader fail before any write.
	tw := tar.NewWriter(&bytes.Buffer{})
	must.Error(writeEntry(tw, "sock", fakeInfo{name: "sock", mode: os.ModeSocket}))
}

func TestWriteEntry_WriteHeaderError(t *testing.T) {
	t.Parallel()
	must := require.New(t)

	file := filepath.Join(t.TempDir(), "real.txt")
	must.NoError(os.WriteFile(file, []byte("data"), 0o644))
	info, err := os.Stat(file)
	must.NoError(err)

	// tar writes the header straight to the failing writer (no gzip buffering).
	tw := tar.NewWriter(failWriter{})
	must.ErrorIs(writeEntry(tw, file, info), errBoom)
}

func TestExtractFile_CopyError(t *testing.T) {
	t.Parallel()
	must := require.New(t)

	target := filepath.Join(t.TempDir(), "out.txt")
	hdr := &tar.Header{Name: "out.txt", Mode: 0o644}

	err := extractFile(target, hdr, failReadCloser{})
	must.ErrorIs(err, ErrExtract)
}

func TestRoundTrip_SingleFile(t *testing.T) {
	t.Parallel()
	want, must := assert.New(t), require.New(t)

	// Create source
	srcDir := t.TempDir()
	must.NoError(os.WriteFile(filepath.Join(srcDir, "hello.txt"), []byte("hello world"), 0o644))

	// Create archive
	var buf bytes.Buffer
	must.NoError(Create(&buf, []string{filepath.Join(srcDir, "hello.txt")}))

	// Extract
	destDir := t.TempDir()
	extracted, err := Extract(&buf, DestDir(destDir))
	must.NoError(err)
	want.NotEmpty(extracted)

	// Verify content
	data, err := os.ReadFile(filepath.Join(destDir, srcDir, "hello.txt"))
	must.NoError(err)
	want.Equal("hello world", string(data))
}

func TestRoundTrip_DirectoryTree(t *testing.T) {
	t.Parallel()
	want, must := assert.New(t), require.New(t)

	// Create source tree
	srcDir := t.TempDir()
	subDir := filepath.Join(srcDir, "sub")
	must.NoError(os.MkdirAll(subDir, 0o755))
	must.NoError(os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("aaa"), 0o644))
	must.NoError(os.WriteFile(filepath.Join(subDir, "b.txt"), []byte("bbb"), 0o644))

	// Create archive of the whole tree
	var buf bytes.Buffer
	must.NoError(Create(&buf, []string{srcDir}))

	// Extract
	destDir := t.TempDir()
	extracted, err := Extract(&buf, DestDir(destDir))
	must.NoError(err)
	want.GreaterOrEqual(len(extracted), 3) // dir + 2 files + subdir

	// Verify contents
	data, err := os.ReadFile(filepath.Join(destDir, srcDir, "a.txt"))
	must.NoError(err)
	want.Equal("aaa", string(data))

	data, err = os.ReadFile(filepath.Join(destDir, srcDir, "sub", "b.txt"))
	must.NoError(err)
	want.Equal("bbb", string(data))
}

func TestList(t *testing.T) {
	t.Parallel()
	want, must := assert.New(t), require.New(t)

	srcDir := t.TempDir()
	must.NoError(os.WriteFile(filepath.Join(srcDir, "one.txt"), []byte("1"), 0o644))
	must.NoError(os.WriteFile(filepath.Join(srcDir, "two.txt"), []byte("2"), 0o644))

	var buf bytes.Buffer
	must.NoError(Create(&buf, []string{srcDir}))

	entries, err := List(&buf)
	must.NoError(err)
	want.GreaterOrEqual(len(entries), 2) // at least the dir + 2 files
}

func TestExtract_PathTraversal(t *testing.T) {
	t.Parallel()
	must := require.New(t)

	data := buildTarGz(t, []tarEntry{
		{name: "../escape.txt", typeflag: tar.TypeReg, mode: 0o644, body: "evil"},
	})

	_, err := Extract(bytes.NewReader(data), DestDir(t.TempDir()))
	must.ErrorIs(err, ErrExtract)
}

func TestExtract_DirEntry(t *testing.T) {
	t.Parallel()
	want, must := assert.New(t), require.New(t)

	data := buildTarGz(t, []tarEntry{
		{name: "subdir", typeflag: tar.TypeDir, mode: 0o755},
		{name: "subdir/file.txt", typeflag: tar.TypeReg, mode: 0o644, body: "hi"},
	})

	dest := t.TempDir()
	extracted, err := Extract(bytes.NewReader(data), DestDir(dest))
	must.NoError(err)
	want.Equal([]string{"subdir", "subdir/file.txt"}, extracted)

	body, err := os.ReadFile(filepath.Join(dest, "subdir", "file.txt"))
	must.NoError(err)
	want.Equal("hi", string(body))
}

func TestExtract_NotGzip(t *testing.T) {
	t.Parallel()
	must := require.New(t)

	_, err := Extract(bytes.NewReader([]byte("not gzip")), DestDir(t.TempDir()))
	must.ErrorIs(err, ErrExtract)
}

func TestExtract_CorruptTar(t *testing.T) {
	t.Parallel()
	must := require.New(t)

	// Valid gzip header wrapping a truncated/garbage tar stream.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	_, err := gw.Write([]byte("this is not a valid tar archive payload"))
	must.NoError(err)
	must.NoError(gw.Close())

	_, err = Extract(bytes.NewReader(buf.Bytes()), DestDir(t.TempDir()))
	must.ErrorIs(err, ErrExtract)
}

func TestExtract_OpenFileError(t *testing.T) {
	t.Parallel()
	must := require.New(t)

	// A regular-file entry whose name collides with an existing directory makes
	// os.OpenFile fail inside extractFile.
	data := buildTarGz(t, []tarEntry{
		{name: "collision", typeflag: tar.TypeReg, mode: 0o644, body: "x"},
	})

	dest := t.TempDir()
	must.NoError(os.MkdirAll(filepath.Join(dest, "collision"), 0o755))

	_, err := Extract(bytes.NewReader(data), DestDir(dest))
	must.ErrorIs(err, ErrExtract)
}

func TestList_NotGzip(t *testing.T) {
	t.Parallel()
	must := require.New(t)

	_, err := List(bytes.NewReader([]byte("not gzip")))
	must.ErrorIs(err, ErrExtract)
}

func TestList_CorruptTar(t *testing.T) {
	t.Parallel()
	must := require.New(t)

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	_, err := gw.Write([]byte("garbage tar payload that is not valid"))
	must.NoError(err)
	must.NoError(gw.Close())

	_, err = List(bytes.NewReader(buf.Bytes()))
	must.ErrorIs(err, ErrExtract)
}

func TestCreate_WriterError(t *testing.T) {
	t.Parallel()
	must := require.New(t)

	// Empty paths still flush the tar/gzip footers, which the failing writer rejects.
	err := Create(failWriter{}, []string{})
	must.ErrorIs(err, ErrCreateArchive)
}

func TestCreate_WriteHeaderError(t *testing.T) {
	t.Parallel()
	must := require.New(t)

	// A real file forces addPath to write a header into the failing writer.
	srcDir := t.TempDir()
	file := filepath.Join(srcDir, "real.txt")
	must.NoError(os.WriteFile(file, []byte("content"), 0o644))

	err := Create(failWriter{}, []string{file})
	must.ErrorIs(err, ErrCreateArchive)
}

func TestExtract_DirMkdirError(t *testing.T) {
	t.Parallel()
	must := require.New(t)

	// A directory entry whose target already exists as a file makes MkdirAll fail.
	data := buildTarGz(t, []tarEntry{
		{name: "clash", typeflag: tar.TypeDir, mode: 0o755},
	})

	dest := t.TempDir()
	must.NoError(os.WriteFile(filepath.Join(dest, "clash"), []byte("file"), 0o644))

	_, err := Extract(bytes.NewReader(data), DestDir(dest))
	must.ErrorIs(err, ErrExtract)
}

func TestExtract_RegfileParentMkdirError(t *testing.T) {
	t.Parallel()
	must := require.New(t)

	// A regular-file entry nested under a path component that exists as a file
	// makes the parent MkdirAll fail.
	data := buildTarGz(t, []tarEntry{
		{name: "parent/child.txt", typeflag: tar.TypeReg, mode: 0o644, body: "x"},
	})

	dest := t.TempDir()
	must.NoError(os.WriteFile(filepath.Join(dest, "parent"), []byte("file"), 0o644))

	_, err := Extract(bytes.NewReader(data), DestDir(dest))
	must.ErrorIs(err, ErrExtract)
}

func TestCreate_NonexistentPath(t *testing.T) {
	t.Parallel()
	must := require.New(t)

	var buf bytes.Buffer
	err := Create(&buf, []string{filepath.Join(t.TempDir(), "does-not-exist")})
	must.ErrorIs(err, ErrCreateArchive)
}

func TestCreate_Symlink(t *testing.T) {
	t.Parallel()
	want, must := assert.New(t), require.New(t)

	if runtime.GOOS == "windows" {
		t.Skip("symlinks require privilege on windows")
	}

	srcDir := t.TempDir()
	target := filepath.Join(srcDir, "target.txt")
	must.NoError(os.WriteFile(target, []byte("data"), 0o644))
	link := filepath.Join(srcDir, "link.txt")
	must.NoError(os.Symlink(target, link))

	var buf bytes.Buffer
	must.NoError(Create(&buf, []string{srcDir}))

	// Walk the raw tar to locate the link entry and assert the symlink contract:
	// buildHeader resolves the symlink to a TypeSymlink header (header.Name is
	// the link's path) whose Linkname preserves the os.Readlink target.
	hdr := findHeader(t, buf.Bytes(), link)
	must.NotNil(hdr)
	want.Equal(byte(tar.TypeSymlink), hdr.Typeflag)
	want.Equal(target, hdr.Linkname)
}

// findHeader decompresses a tar.gz and returns the header whose Name equals
// name, or nil if no such entry exists.
func findHeader(t *testing.T, data []byte, name string) *tar.Header {
	t.Helper()
	gr, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	defer func() { require.NoError(t, gr.Close()) }()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		require.NoError(t, err)
		if hdr.Name == name {
			return hdr
		}
	}
}

func TestCreate_UnreadableFile(t *testing.T) {
	t.Parallel()
	must := require.New(t)

	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions")
	}

	srcDir := t.TempDir()
	secret := filepath.Join(srcDir, "secret.txt")
	must.NoError(os.WriteFile(secret, []byte("nope"), 0o000))

	var buf bytes.Buffer
	err := Create(&buf, []string{secret})
	must.ErrorIs(err, ErrCreateArchive)
}

func TestCreate_EmptyPaths(t *testing.T) {
	t.Parallel()
	must := require.New(t)

	var buf bytes.Buffer
	must.NoError(Create(&buf, []string{}))

	entries, err := List(&buf)
	must.NoError(err)
	must.Empty(entries)
}
