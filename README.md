# git-expunge

[![Build](https://github.com/benjaminabbitt/git-expunge/actions/workflows/build.yml/badge.svg)](https://github.com/benjaminabbitt/git-expunge/actions/workflows/build.yml)
[![Release](https://img.shields.io/github/v/release/benjaminabbitt/git-expunge)](https://github.com/benjaminabbitt/git-expunge/releases/latest)
[![License](https://img.shields.io/github/license/benjaminabbitt/git-expunge)](LICENSE)

> Retroactively clean Git history against your current `.gitignore`, plus remove secrets, binaries, and oversized blobs — safely.

> [!WARNING]
> **Pre-release software** - While git-expunge has been successfully used on real repositories, it is still in early development. **Always back up your entire repository before using this tool.** git-expunge creates automatic backups before rewriting history, but an independent backup is strongly recommended. History rewriting is a destructive operation that cannot be undone.

**git-expunge** is a stateless CLI for removing things that should never have been committed in the first place. Its headline use case: **retroactively apply your current `.gitignore` to your entire history**. Files that are ignored today were almost always ignored "after the fact" — they leaked in early and the `.gitignore` got patched later. git-expunge walks history, asks `git check-ignore` what would be ignored under today's rules, and lets you remove anything that matches in a single audited rewrite.

It also runs gitleaks for secret detection, plus optional binary and large-file detectors. Findings land in a JSON manifest (`git-expunge-findings.json`); `git-expunge list` renders it as tab-separated rows so you can pipe through `grep`/`awk`, and `remove` lets you curate by glob before the rewrite.

## Retroactively scrub everything your `.gitignore` would have caught

This is the workflow git-expunge is built around. Run it on any repo where the `.gitignore` was added (or hardened) after files had already been committed — almost every repo, in practice.

```bash
cd /path/to/repo

# 1. Make absolutely sure you have an independent backup.
#    git-expunge will make its own archive too, but trust nothing.
git bundle create ../$(basename $PWD)-pre-expunge.bundle --all

# 2. Build a manifest of every historical blob whose path is ignored today.
#    Default scan = secrets + gitignored, so secrets get caught in the same pass.
git-expunge scan
#   → writes git-expunge-findings.json
#   → exits 1 if anything was added (CI-friendly)

# 3. Eyeball the manifest. `list` prints tab-separated rows
#    (type, hash, size, path) from the on-disk JSON manifest.
git-expunge list | head -40
git-expunge summary

# 4. Drop false positives by glob. The manifest is just JSON,
#    and `remove` is the curated way to prune it.
git-expunge remove "vendor/**" "third_party/**"

# 5. Dry-run the rewrite. Nothing is touched yet.
git-expunge rewrite

# 6. Execute. Creates a full backup archive next to the repo first.
git-expunge rewrite --execute

# 7. Verify the bad blobs are unreachable in the new history.
git-expunge verify

# 8. Force-push (you must coordinate with collaborators — every
#    contributor has to re-clone afterwards).
git push --force --all
git push --force --tags
```

The expected day-one outcome on a long-lived repo: `scan` flags a pile of legitimate-but-historical `.env` files, build artifacts, log files, and IDE droppings; `remove` lets you cut down to the actual problem set; the rewrite cleans them out for good.

> [!IMPORTANT]
> Rewriting history is a coordinated, destructive operation. Every collaborator must re-clone after a force-push. Don't run this on a repo with many active contributors without a plan.

## Other use cases

| Scenario                                                                            | Command                                  |
| ----------------------------------------------------------------------------------- | ---------------------------------------- |
| Remove accidentally committed `.env` with DB passwords                              | `git-expunge scan secrets`               |
| Delete a specific file or glob across all history                                   | `git-expunge add "vendor/secrets.json" .` |
| Purge large binary blobs from history                                               | `git-expunge scan large --size 5MB`      |
| Drop every binary file (any size, any MIME) — e.g. checking-in build artifacts      | `git-expunge scan binaries`              |
| Run everything at once                                                              | `git-expunge scan all`                   |
| Use in CI to fail the build when a new secret lands                                 | `git-expunge scan secrets --json` (exit 1 = fresh finding) |

## Why git-expunge?

| Feature                | git-expunge          | BFG    | git-filter-repo |
| ---------------------- | -------------------- | ------ | --------------- |
| Language               | Go (single binary)   | Java   | Python          |
| Full backup            | Yes (archive)        | No     | No              |
| Secret detection       | Built-in (gitleaks)  | No     | No              |
| Binary detection       | Built-in (opt-in)    | Manual | Manual          |
| **Gitignore replay**   | **Built-in**         | **No** | **No**          |
| Dry-run by default     | Yes                  | No     | Yes             |
| CI-friendly exit codes | Yes (0 / 1 / 2)      | No     | Partial         |

## Installation

### Linux (x64)

```bash
curl -LO https://github.com/benjaminabbitt/git-expunge/releases/latest/download/git-expunge-linux-amd64.tar.gz
tar xzf git-expunge-linux-amd64.tar.gz
sudo mv git-expunge-linux-amd64 /usr/local/bin/git-expunge
```

### macOS

```bash
# Apple Silicon
curl -LO https://github.com/benjaminabbitt/git-expunge/releases/latest/download/git-expunge-darwin-arm64.tar.gz
tar xzf git-expunge-darwin-arm64.tar.gz
sudo mv git-expunge-darwin-arm64 /usr/local/bin/git-expunge

# Intel
curl -LO https://github.com/benjaminabbitt/git-expunge/releases/latest/download/git-expunge-darwin-amd64.tar.gz
tar xzf git-expunge-darwin-amd64.tar.gz
sudo mv git-expunge-darwin-amd64 /usr/local/bin/git-expunge
```

### Windows

```powershell
Invoke-WebRequest -Uri https://github.com/benjaminabbitt/git-expunge/releases/latest/download/git-expunge-windows-amd64.zip -OutFile git-expunge.zip
Expand-Archive git-expunge.zip -DestinationPath .
Move-Item git-expunge-windows-amd64.exe git-expunge.exe
```

### From source

```bash
go install github.com/benjaminabbitt/git-expunge/cmd/git-expunge@latest
```

### All releases

Download from the [releases page](https://github.com/benjaminabbitt/git-expunge/releases) - binaries are statically linked with no runtime dependencies.

## Quick start

```bash
cd /path/to/repo

git-expunge scan                         # safe defaults: secrets + gitignored
git-expunge list                         # inspect what landed in the manifest
git-expunge remove "vendor/**"           # drop false positives by glob
git-expunge rewrite                      # dry-run
git-expunge rewrite --execute            # do it (creates a full backup first)
git-expunge verify                       # confirm the bad blobs are gone
```

## Core model

The on-disk manifest (`git-expunge-findings.json`) is the single source of truth. **Membership in the manifest IS the intent** — every entry will be removed on the next `git-expunge rewrite --execute`. There is no "mark for purge" flag. Curate the manifest by adding (`scan`, `add`) and removing (`remove`) entries.

Commands are stateless: each invocation reads-or-writes the manifest in one shot. No daemon, no carried session.

## Commands

| Command                                 | Purpose                                                                                       |
| --------------------------------------- | --------------------------------------------------------------------------------------------- |
| `git-expunge scan [detector...] [repo]` | Detect content for the manifest. Detectors: `secrets`, `gitignored`, `binaries`, `large`, `all`. No detector named ⇒ safe defaults (`secrets`+`gitignored`). |
| `git-expunge add <glob>... [repo]`      | Add specific paths or globs to the manifest                                                   |
| `git-expunge list [repo]`               | Tab-separated dump of every manifest entry; filter with grep/awk                              |
| `git-expunge search <glob>... [repo]`   | Preview history-glob matches without writing the manifest                                     |
| `git-expunge remove <glob>... [repo]`   | Drop manifest entries whose path matches a glob                                               |
| `git-expunge summary [repo]`            | Counts by finding type                                                                        |
| `git-expunge preview <hash> [repo]`     | Show a blob's content (hex dump for binaries, text otherwise)                                 |
| `git-expunge report generate \| read`   | Markdown round-trip for offline review                                                        |
| `git-expunge rewrite [repo]`            | Dry-run by default; pass `--execute` to actually rewrite (creates a backup first)             |
| `git-expunge verify [repo]`             | Confirm purged blobs are unreachable after a rewrite                                          |
| `git-expunge restore [repo] --archive`  | Restore from a backup archive                                                                 |

### Scanning

Each detector covers an orthogonal axis. They can be combined; multiple detectors share a single history walk where possible.

| Detector     | What it flags                                                                              |
| ------------ | ------------------------------------------------------------------------------------------ |
| `secrets`    | API keys, passwords, tokens, private keys (gitleaks rules)                                 |
| `gitignored` | Paths matching the repository's current gitignore rules (per-directory, info/exclude, core.excludesFile) |
| `binaries`   | Anything with a binary MIME / magic-byte signature, **regardless of size**                 |
| `large`      | Anything larger than `--size` (default 100KB), **regardless of MIME**                      |
| `all`        | Every detector above                                                                       |

The safe default (no detector named) runs `secrets` + `gitignored` only — these are high-confidence. `binaries` and `large` are noisy on real repositories so they require opt-in.

```bash
git-expunge scan                                  # secrets + gitignored
git-expunge scan secrets                          # secrets only
git-expunge scan secrets gitignored .             # both (single walk), repo "."
git-expunge scan binaries large --size 1MB        # both binaries AND large (>1MB)
git-expunge scan all                              # everything
```

**`--json`** emits the delta findings as a JSON array on stdout and suppresses the human-readable summary. The manifest is still written. Useful for piping into other tooling.

### Exit codes (use in CI)

`scan` is the only command with structured exit codes. Other commands follow the cobra default (0 success, 1 error).

| Exit | Meaning                                                              | Use in CI                                                 |
| ---- | -------------------------------------------------------------------- | --------------------------------------------------------- |
| `0`  | Success, **no new findings** added to the manifest                   | Build passes — nothing new to look at                     |
| `1`  | Success, **one or more new findings** added to the manifest          | Fail the build / open an issue / page someone             |
| `2`  | **Tool error** — bad flag, unknown detector, git failure, missing repo | Treat as infrastructure problem, not a code problem       |

The `1` ↔ `2` split matters: a tool failure shouldn't be misinterpreted as "we found new secrets." Distinguish them in your CI step.

```bash
# Simplest: any non-zero is a failure.
git-expunge scan secrets || exit 1

# Better: distinguish "new findings" (signal) from "tool broke" (infrastructure).
set +e
git-expunge scan secrets
ec=$?
set -e
case $ec in
  0) echo "clean" ;;
  1) echo "::error::new secrets in history — see git-expunge-findings.json" ; exit 1 ;;
  2) echo "::error::git-expunge itself failed" ; exit 2 ;;
esac
```

GitHub Actions example using `--json` to attach the delta to the run:

```yaml
- name: Scan for new secrets in history
  id: scan
  shell: bash
  run: |
    # Capture the exit code without tripping bash's default -e.
    set +e
    git-expunge scan secrets --json > new-findings.json
    ec=$?
    set -e
    echo "exit_code=$ec" >> "$GITHUB_OUTPUT"
- name: Upload findings
  if: steps.scan.outputs.exit_code == '1'
  uses: actions/upload-artifact@v4
  with:
    name: git-expunge-findings
    path: new-findings.json
- name: Fail on new findings
  if: steps.scan.outputs.exit_code == '1'
  run: exit 1
- name: Fail on tool error
  if: steps.scan.outputs.exit_code == '2'
  run: |
    echo "::error::git-expunge itself failed; check job logs"
    exit 1
```

Because scan is idempotent (running it twice with no new commits adds nothing), the same step is safe to run on every push: it'll exit `0` until something new lands.

### Adding specific paths

```bash
git-expunge add vendor/secrets.json .       # exact path
git-expunge add "*.env" .                   # glob (quote to prevent shell expansion)
git-expunge add "vendor/**" .               # recursive
git-expunge add "*.pem" "*.key" ".env*" .   # multiple patterns
```

### Searching, filtering, removing

There is no query DSL. `list` prints tab-separated rows; pipe through grep/awk to filter:

```bash
git-expunge list | grep secret                                # only secrets
git-expunge list | awk -F'\t' '$3 > 1048576 { print }'        # entries over 1 MB
git-expunge summary                                            # counts by type
git-expunge search "*.log" .                                   # preview without touching manifest
git-expunge remove "vendor/**" "tests/**"                      # drop false positives by glob
```

### Rewriting

- Creates a **full backup archive** before any changes (skip with `--skip-backup`, dangerous).
- Uses `git fast-export/fast-import` for reliable history rewriting.
- Runs in **dry-run mode by default** — shows what would happen.
- Only executes when you explicitly pass `--execute`.

After a successful execute, the rewritten entries are moved from the active manifest into a sidecar at `.git/git-expunge-last-purged.json`. `verify` consults the sidecar to confirm the blobs are unreachable.

### Verifying

```bash
git-expunge verify .
```

`verify` only ever reads the post-rewrite sidecar. It deliberately does **not** fall back to the active manifest — an entry there means "the user intends to remove this", not "this was removed", so verifying intent would be misleading.

## Worktree support

git-expunge fully supports repositories with multiple worktrees. When rewriting history:

1. **All worktrees are detected** via `.git/worktrees/`.
2. **State is cleaned up** — each worktree's index and reflogs are updated to reference new commits.
3. **Working trees are reset** to match the new history.

**Important:** After a rewrite, all worktrees will be reset to their branch's HEAD. Any uncommitted changes in worktrees will be lost. Commit or stash before running a rewrite.

```bash
git worktree list
cd /path/to/worktree && git add . && git commit -m "WIP before rewrite"
```

## Safety

git-expunge is designed to never lose your data:

- **Full archive backup** before any rewrite.
- **Dry-run by default** — shows what would happen without making changes.
- **Restore command** to recover from backup.
- **Verification** confirms purged data is unreachable after rewrite.

## Development

```bash
git clone https://github.com/benjaminabbitt/git-expunge
cd git-expunge

just deps          # install deps
just test          # run tests
just build         # build → ./bin/git-expunge
./bin/git-expunge scan /path/to/repo
```

`just --list` shows every available target (`build`, `test`, `test-unit`, `test-integration`, `lint`, `fmt`, `cover`, `verify`, `build-all`, …).

## Contributing

Issues and PRs welcome. Please open an issue first to discuss anything beyond a small fix.

## License

MIT License - see [LICENSE](LICENSE) for details.

## Related tools

- [BFG Repo-Cleaner](https://rtyley.github.io/bfg-repo-cleaner/) - Fast, simple tool (Java)
- [git-filter-repo](https://github.com/newren/git-filter-repo) - Powerful rewriting tool (Python)
- [gitleaks](https://github.com/gitleaks/gitleaks) - Secret scanning (used by git-expunge)

---

**Keywords**: remove sensitive data from git history, delete files from git history, git remove large files, git remove secrets, git history cleaner, remove API keys from git, git purge binary files, BFG alternative, git-filter-repo alternative, remove accidentally committed files git, clean git repository history
