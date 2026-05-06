# Quick Start Guide

## Installation

The consolidate-dependabot skill is already integrated into your project. To use it:

### Option 1: Ask Copilot (Recommended)

```
/consolidate-dependabot all
```

Or specify specific PRs:
```
/consolidate-dependabot 117,120,123
```

### Option 2: Run Script Directly

```bash
cd .github/skills/consolidate-dependabot/scripts
chmod +x consolidate-dependabot.sh
./consolidate-dependabot.sh all
```

## Common Tasks

### Consolidate All Open Dependabot PRs

```bash
/consolidate-dependabot all
```

**What it does:**
1. Finds all open dependabot PRs
2. Creates `feat/dependency-update` branch
3. Merges all PRs (resolves conflicts automatically)
4. Runs tests (`go test ./...`)
5. Runs static checks (`go vet`, `golangci-lint`, etc.)
6. Pushes branch and creates PR

**Time**: ~2-5 minutes depending on test suite

### Consolidate Specific PRs

```bash
/consolidate-dependabot 117,120,123
```

Merges only PRs #117, #120, and #123.

### Consolidate Without Tests (Fast Track)

```bash
/consolidate-dependabot --skip-tests all
```

Use only when you know the changes are safe or tests are broken unrelated to dependencies.

### Dry Run (Preview)

```bash
/consolidate-dependabot --dry-run all
```

Shows what would happen without making any changes.

## Understanding the Output

### Success Output

```
ℹ Consolidating Dependabot Updates
ℹ Fetching pull requests...
✓ Found PRs: 117,120,123
ℹ Creating consolidation branch: feat/dependency-update
✓ Created branch: feat/dependency-update
ℹ Merging 3 dependabot PR(s)...
✓ Merged PR #117
✓ Merged PR #120
✓ Merged PR #123
✓ Merged 3 PR(s)
ℹ Running tests...
✓ Tests passed
ℹ Running static checks...
✓ go vet passed
✓ go mod tidy passed
✓ golangci-lint passed
ℹ Pushing feat/dependency-update to origin...
✓ Pushed to origin/feat/dependency-update
ℹ Creating pull request...
✓ Created pull request
✓ Consolidation complete!
```

### Error Output

```
✗ Tests failed
```

**What to do:**
1. Check test output for specific failures
2. Review if new dependency versions have breaking changes
3. Either update code or split consolidation into smaller batches
4. See [Troubleshooting Guide](./references/troubleshooting.md)

## Files and Structure

```
.github/skills/consolidate-dependabot/
├── SKILL.md                          # Main skill documentation
├── scripts/
│   └── consolidate-dependabot.sh     # Executable script
├── templates/
│   ├── pr-description-go.md          # Go/Rust projects
│   └── pr-description-node.md        # Node.js projects
└── references/
    ├── troubleshooting.md            # Common issues and fixes
    └── dependency-matrix.md          # How to format dependency docs
```

## Next Steps

- **Review the created PR** on GitHub
- **Merge when ready** (after team review)
- **Schedule regular consolidations** (weekly/bi-weekly)
- **Document your version strategy** in CONTRIBUTING.md

## More Information

- See full instructions in [SKILL.md](./SKILL.md)
- Troubleshooting: [troubleshooting.md](./references/troubleshooting.md)
- Dependency matrix format: [dependency-matrix.md](./references/dependency-matrix.md)
