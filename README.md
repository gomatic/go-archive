# go-archive

Stream-oriented `tar.gz` archiving for Go: create an archive from filesystem paths, list its entries, or extract it into a directory — with built-in path-traversal ("zip-slip") protection. Works over any `io.Writer`/`io.Reader`, depends only on the standard library, and reports every failure as a sentinel matchable with `errors.Is`.

## Install

```sh
go get github.com/gomatic/go-archive
```

## Usage

```go
package main

import (
	"bytes"
	"fmt"

	"github.com/gomatic/go-archive"
)

func main() {
	// Create a tar.gz of some paths.
	var buf bytes.Buffer
	if err := archive.Create(&buf, []string{"./docs", "./README.md"}); err != nil {
		panic(err)
	}

	// List its contents without writing anything.
	names, err := archive.List(bytes.NewReader(buf.Bytes()))
	if err != nil {
		panic(err)
	}
	fmt.Println(names)

	// Extract it into a directory (path traversal is rejected with ErrExtract).
	extracted, err := archive.Extract(bytes.NewReader(buf.Bytes()), "./out")
	if err != nil {
		panic(err)
	}
	fmt.Println(extracted)
}
```

## Errors

Every failure wraps one of the package sentinels, recoverable with `errors.Is`:

- `archive.ErrCreateArchive` — a path could not be walked or written into the archive.
- `archive.ErrExtract` — the stream was not a valid `tar.gz`, an entry attempted path traversal, or an entry could not be written.

## Build & test

The `Makefile`, `.golangci.yaml`, `.editorconfig`, `.gitignore`, and `.github/` are the canonical gomatic Go toolchain, owned and distributed by [`nicerobot/tools.repository`](https://github.com/nicerobot/tools.repository) — do not edit them in-tree; per-repo changes belong in a `Makefile.local`. Run the full gate (lint, staticcheck, govulncheck, 100% coverage) with `make check`.
