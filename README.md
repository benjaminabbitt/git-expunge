# git-expunge

[![Build](https://github.com/benjaminabbitt/git-expunge/actions/workflows/build.yml/badge.svg)](https://github.com/benjaminabbitt/git-expunge/actions/workflows/build.yml)
[![Release](https://img.shields.io/github/v/release/benjaminabbitt/git-expunge)](https://github.com/benjaminabbitt/git-expunge/releases/latest)
[![License](https://img.shields.io/github/license/benjaminabbitt/git-expunge)](LICENSE)

> Safely remove sensitive data and large files from Git history

**git-expunge** is a user-friendly tool for removing accidentally committed secrets, API keys, binary files, and other sensitive data from your Git repository history. Unlike other tools, it prioritizes safety with full backup/restore capabilities and provides multiple interfaces for reviewing findings before making any destructive changes.

## Why git-expunge?

| Feature | git-expunge | BFG | git-filter-repo |
|---------|-------------|-----|-----------------|
| Language | Go (single binary) | Java | Python |
| Full backup | Yes (archive) | No | No |
| Secret detection | Built-in (gitleaks) | No | No |
| Binary detection | Built-in | Manual | Manual |
| Interactive review | TUI + CLI + Report | No | No |
| Dry-run by default | Yes | No | Yes |

## Common Use Cases

- **Remove accidentally committed `.env` files** with database passwords
- **Delete AWS keys, API tokens** from repository history
- **Purge large binary files** (builds, assets, compiled artifacts)
- **Clean up repository** before making it public or open source

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

## Quick Start

```bash
# Launch interactive TUI - browse files, scan for secrets, review, and rewrite
cd /path/to/repo
git-expunge

# Or use CLI commands:
git-expunge scan .                # Scan for secrets and binaries
git-expunge add "*.env" .         # Add specific files/patterns to remove
git-expunge rewrite .             # Dry-run rewrite
git-expunge rewrite . --execute   # Execute the rewrite
```

## How It Works

### 1. Scan

Scans your entire Git history for:
- **Secrets**: API keys, passwords, tokens, private keys (using gitleaks rules)
- **Binaries**: Executable files, compiled artifacts, large binary blobs

Outputs a `git-expunge-findings.json` file listing all findings.

### 2. Review

Three ways to review and select what to remove:

- **TUI**: Interactive terminal interface with keyboard navigation
- **CLI**: Simple command-line prompts
- **Report**: Generate a markdown file, edit it in any text editor

#### TUI Modes

The TUI has four modes accessible via number keys (1-4) or tab:

| Mode | Key | Description |
|------|-----|-------------|
| Review | `1` | View detected findings, mark items for purge |
| Browse | `2` | Browse all historical files as a directory tree, select to add |
| Scan | `3` | Run scanner to detect secrets/binaries |
| Rewrite | `4` | Dry-run or execute history rewrite |

**Key bindings:**
- `1-4` or `tab`: Switch modes
- `↑/↓`: Navigate
- `space`: Toggle selection
- `a`: Select all, `c`: Clear all
- `s`: Save manifest
- `q`: Quit

In Browse mode:
- `←/→`: Collapse/expand directories
- `/`: Search files

### 3. Rewrite

- Creates a **full backup archive** before any changes
- Uses `git fast-export/fast-import` for reliable history rewriting
- Runs in **dry-run mode by default** - shows what would happen
- Only executes when you explicitly pass `--execute`

### 4. Verify

After rewriting, verify that the sensitive data is truly gone:

```bash
git-expunge verify /path/to/repo --manifest git-expunge-findings.json
```

## Worktree Support

git-expunge fully supports repositories with multiple worktrees. When rewriting history:

1. **All worktrees are detected** - git-expunge finds all linked worktrees via `.git/worktrees/`
2. **State is cleaned up** - Each worktree's index and reflogs are updated to reference new commits
3. **Working trees are reset** - All worktrees are reset to match the new history

**Important**: After a rewrite, all worktrees will be reset to their branch's HEAD. Any uncommitted changes in worktrees will be lost. Make sure to commit or stash any work before running a rewrite.

```bash
# List your worktrees before rewriting
git worktree list

# Commit any work in worktrees
cd /path/to/worktree && git add . && git commit -m "WIP before rewrite"
```

## Safety First

git-expunge is designed to never lose your data:

- **Full archive backup**: Creates a compressed backup of your entire repository before any rewrite
- **Dry-run by default**: Shows what would happen without making changes
- **Restore command**: Easily restore from backup if anything goes wrong
- **Verification**: Confirms purged data is unreachable after rewrite

## Commands

```
git-expunge [repo]            Launch interactive TUI (default command)
git-expunge scan [repo]       Scan for secrets and binaries
git-expunge add <path>...     Add files/directories to manifest for removal
git-expunge report generate   Create human-readable markdown report
git-expunge report read       Parse markdown back to manifest
git-expunge rewrite           Rewrite history to remove selected items
git-expunge verify            Verify purged items are gone
git-expunge restore           Restore from backup archive
```

### Adding Arbitrary Files

Use `git-expunge add` to remove specific files or directories from history:

```bash
# Add exact paths
git-expunge add vendor/secrets.json .

# Add glob patterns (quote to prevent shell expansion)
git-expunge add "*.env" .
git-expunge add "vendor/**" .

# Multiple patterns
git-expunge add "*.pem" "*.key" ".env*" .
```

Added files are stored in the manifest with `purge: true` and will be removed on the next rewrite.

## Configuration

Create a `.git-expunge.yaml` in your repository to customize detection rules:

```yaml
# Custom size threshold for binary detection
binary:
  size_threshold: 50KB

# Additional secret patterns
secrets:
  rules:
    - id: custom-api-key
      regex: 'MY_API_KEY=[A-Za-z0-9]{32}'
      description: "Custom API key pattern"
```

## Development

```bash
# Clone the repository
git clone https://github.com/benjaminabbitt/git-expunge
cd git-expunge

# Install dependencies
just deps

# Run tests
just test

# Build
just build

# Run
just run scan /path/to/repo
```

## Contributing

Contributions are welcome! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

MIT License - see [LICENSE](LICENSE) for details.

## Related Tools

- [BFG Repo-Cleaner](https://rtyley.github.io/bfg-repo-cleaner/) - Fast, simple tool (Java)
- [git-filter-repo](https://github.com/newren/git-filter-repo) - Powerful rewriting tool (Python)
- [gitleaks](https://github.com/gitleaks/gitleaks) - Secret scanning (used by git-expunge)

---

**Keywords**: remove sensitive data from git history, delete files from git history, git remove large files, git remove secrets, git history cleaner, remove API keys from git, git purge binary files, BFG alternative, git-filter-repo alternative, remove accidentally committed files git, clean git repository history
