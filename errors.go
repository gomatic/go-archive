package archive

import "fmt"

// Error is archive's sentinel-error type. Every error the package can emit is
// declared as a const of this type, so each one is matchable with errors.Is
// rather than by string comparison. It follows the same shape as the rest of
// the ecosystem's sentinel-error helpers.
type Error string

// Error returns the constant's text, implementing the error interface.
func (e Error) Error() string { return string(e) }

var _ error = Error("")

// Wrap returns an error that always carries the sentinel e in its chain (so
// errors.Is(result, e) holds), optionally annotated with context args and a
// wrapped cause. The sentinel e itself is preserved unchanged in the chain;
// context is added as a separate message layer so identity is never lost.
func (e Error) Wrap(err error, args ...any) error {
	switch {
	case len(args) > 0 && err != nil:
		return fmt.Errorf("%w: %s: %w", e, fmt.Sprint(args...), err)
	case len(args) > 0:
		return fmt.Errorf("%w: %s", e, fmt.Sprint(args...))
	case err != nil:
		return fmt.Errorf("%w: %w", e, err)
	default:
		return e
	}
}

const (
	// ErrCreateArchive is the leading sentinel wrapped when a tar.gz archive
	// cannot be created.
	ErrCreateArchive Error = "failed to create archive"
	// ErrExtract is the leading sentinel wrapped when a tar.gz archive cannot be
	// read, listed, or extracted.
	ErrExtract Error = "failed to extract"
	// ErrPathTraversal is the sentinel for a malicious archive entry whose path or
	// symlink target escapes the destination directory. It is distinct from
	// ErrExtract so callers can tell a hostile archive from an I/O fault.
	ErrPathTraversal Error = "archive entry escapes destination"
)
