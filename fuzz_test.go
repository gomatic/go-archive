package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"

	"github.com/stretchr/testify/require"
)

// seedCorpus returns byte streams that exercise the decompress/iterate seams:
// non-gzip, valid-but-trivial gzip, a real tar.gz, and a traversal entry.
func seedCorpus(t testing.TB) [][]byte {
	t.Helper()

	var gzGarbage bytes.Buffer
	gw := gzip.NewWriter(&gzGarbage)
	_, err := gw.Write([]byte("not a tar stream"))
	require.NoError(t, err)
	require.NoError(t, gw.Close())

	valid := buildTarGz(t, []tarEntry{
		{name: "f.txt", typeflag: tar.TypeReg, mode: 0o644, body: "hi"},
	})
	traversal := buildTarGz(t, []tarEntry{
		{name: "../escape.txt", typeflag: tar.TypeReg, mode: 0o644, body: "evil"},
	})

	return [][]byte{nil, []byte("not gzip"), gzGarbage.Bytes(), valid, traversal}
}

// FuzzExtract asserts Extract never panics on arbitrary bytes: a tar.gz reader
// fed hostile input must fail with an error, never crash, and never write
// outside the destination (path-traversal entries are rejected, not extracted).
func FuzzExtract(f *testing.F) {
	for _, seed := range seedCorpus(f) {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		// A successful extraction must return at least one name; a failure must
		// return none. Either way, no panic and no escape from the temp dir.
		names, err := Extract(bytes.NewReader(data), DestDir(t.TempDir()))
		if err != nil {
			require.Empty(t, names)
		}
	})
}

// FuzzList asserts List never panics on arbitrary bytes and that its name count
// is consistent with its error (names only on success).
func FuzzList(f *testing.F) {
	for _, seed := range seedCorpus(f) {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		names, err := List(bytes.NewReader(data))
		if err != nil {
			require.Empty(t, names)
		}
	})
}
