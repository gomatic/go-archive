# go-archive

Stream-oriented `tar.gz` archiving: `Create` (paths → `io.Writer`), `Extract` (`io.Reader` → dir, with zip-slip protection), and `List`. Extracted from `gomatic/ssh-tgzx`'s `internal/archive`.

- Package `archive`, generic and dependency-free (stdlib only; testify for tests). Every failure wraps a sentinel `Error` const (`ErrCreateArchive`, `ErrExtract`) matchable with `errors.Is`.
- Gate: gofumpt, vet, staticcheck, govulncheck, gocognit ≤ 7, 100% coverage. Shared config (`Makefile`, `.golangci.yaml`, `.github/`, …) is owned by `nicerobot/tools.repository` — never edit in-tree; use `Makefile.local`.
- **`.golangci.yaml` divergence:** this repo adds one local gosec exclude — `G305` (zip-slip) — because `extractEntry` already guards every entry with `withinDir` (tested by `TestExtract_PathTraversal`) and gosec flags the unavoidable `filepath.Join` regardless. A `tools.repository` distribute-push resets `.golangci.yaml`; **re-add the `G305` exclude after any such reset** or the gate goes red on a known false positive. (Durable fix would be excluding `G305` upstream in `tools.build`, like `G115`.)
