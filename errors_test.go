package archive

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSentinels_MatchThroughWith verifies the package's error contract: every
// sentinel the package can emit stays matchable with errors.Is after being
// wrapped through errs.Const.With, with or without a cause and context args,
// and never matches a different sentinel. The wrapping mechanism itself is
// owned and tested by github.com/gomatic/go-error.
func TestSentinels_MatchThroughWith(t *testing.T) {
	t.Parallel()

	cause := errors.New("root cause")

	tests := []struct {
		err       error
		want      error
		notWant   error
		name      string
		wantCause bool
	}{
		{
			name:    "ErrCreateArchive bare",
			err:     ErrCreateArchive.With(nil),
			want:    ErrCreateArchive,
			notWant: ErrExtract,
		},
		{
			name:    "ErrCreateArchive with args",
			err:     ErrCreateArchive.With(nil, "path/to/file"),
			want:    ErrCreateArchive,
			notWant: ErrExtract,
		},
		{
			name:      "ErrExtract with cause",
			err:       ErrExtract.With(cause),
			want:      ErrExtract,
			notWant:   ErrCreateArchive,
			wantCause: true,
		},
		{
			name:      "ErrExtract with cause and args",
			err:       ErrExtract.With(cause, "path/to/file"),
			want:      ErrExtract,
			notWant:   ErrPathTraversal,
			wantCause: true,
		},
		{
			name:    "ErrPathTraversal with args",
			err:     ErrPathTraversal.With(nil, "entry"),
			want:    ErrPathTraversal,
			notWant: ErrExtract,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			want := assert.New(t)

			// The sentinel is always recoverable from the chain.
			want.ErrorIs(tt.err, tt.want)
			// A different sentinel must not match.
			want.NotErrorIs(tt.err, tt.notWant)

			if tt.wantCause {
				// The wrapped cause stays recoverable alongside the sentinel.
				want.ErrorIs(tt.err, cause)
			}
		})
	}
}
