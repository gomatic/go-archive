package archive

import errs "github.com/gomatic/go-error"

// Every error the package can emit is declared as a const of the ecosystem's
// sentinel-error type errs.Const, so each one is matchable with errors.Is
// rather than by string comparison. Context and causes are attached with
// errs.Const.With, which keeps both the sentinel and the cause in the chain.
const (
	// ErrCreateArchive is the leading sentinel wrapped when a tar.gz archive
	// cannot be created.
	ErrCreateArchive errs.Const = "failed to create archive"
	// ErrExtract is the leading sentinel wrapped when a tar.gz archive cannot be
	// read, listed, or extracted.
	ErrExtract errs.Const = "failed to extract"
	// ErrPathTraversal is the sentinel for a malicious archive entry whose path or
	// symlink target escapes the destination directory. It is distinct from
	// ErrExtract so callers can tell a hostile archive from an I/O fault.
	ErrPathTraversal errs.Const = "archive entry escapes destination"
)
