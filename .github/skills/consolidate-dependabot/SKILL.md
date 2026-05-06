---
name: consolidate-dependabot
description: 'Consolidate multiple dependabot pull requests into a single managed branch. Merges all open dependabot PRs, runs tests and static checks, then creates a comprehensive PR with documentation. Use when: managing many dependency updates, ensuring consistency across versions, reducing PR noise by batching updates, coordinating team review of dependency changes.'
argument-hint: 'optional: pr-numbers (comma-separated) or "all" for open PRs'
user-invocable: true
---

# Consolidate Dependabot Updates

Streamline dependency management by consolidating multiple dependabot PRs into a single, well-tested branch with comprehensive documentation.

## When to Use

- **Multiple dependabot PRs pending**: Combine 3+ open dependency update PRs
- **Bulk dependency management**: Batch updates for coordinated testing and review
- **Reducing PR noise**: Consolidate updates into a single review point
- **Cross-team coordination**: Ensure all dependency changes are tested together before merging
- **Automated workflows**: Schedule regular dependency consolidation without manual PR management

## What This Skill Does

1. **Identifies PRs**: Discovers all open dependabot PRs (or specific PRs you specify)
2. **Creates branch**: Sets up a `feat/dependency-update` consolidation branch
3. **Merges PRs**: Intelligently merges all selected PRs with conflict resolution
4. **Validates changes**: Runs project-specific tests and static checks
5. **Documents updates**: Analyzes merged dependencies and updates project docs
6. **Creates PR**: Raises a comprehensive PR to main with auto-generated or template-based description

## Prerequisites

- Git repository with GitHub integration
- Open dependabot pull requests
- Project-specific test and lint scripts (npm test, go test, cargo test, etc.)
- PR template file (optional, auto-generates if not found)

## Procedure

### Quick Start

1. **List open dependabot PRs**
   ```
   /consolidate-dependabot
   ```
   The skill will show all pending dependabot PRs.

2. **Consolidate all PRs**
   ```
   /consolidate-dependabot all
   ```
   Merges all open dependabot PRs and creates the consolidation branch.

3. **Consolidate specific PRs**
   ```
   /consolidate-dependabot 117,120,123
   ```
   Merges only the specified PR numbers.

### Step-by-Step Manual Process

If you prefer manual control, follow these steps:

1. **Fetch and Review PRs**
   - Run `git fetch origin` to sync all branches
   - List all open dependabot PRs using GitHub CLI or the API
   - Note PR numbers and affected packages

2. **Create Consolidation Branch**
   ```bash
   git checkout main
   git pull origin main
   git checkout -b feat/dependency-update
   ```

3. **Merge PRs in Sequence**
   - For each PR in order (start with smallest impact):
     ```bash
     git fetch origin pull/PR_NUMBER/head
     git merge FETCH_HEAD
     ```
   - Resolve conflicts with `go mod tidy` or equivalent
   - Commit: `git commit -m "chore: merge dependency updates"`

4. **Run Tests and Checks**
   - Execute full test suite: `go test ./...` (or project equivalent)
   - Run linters: `golangci-lint run`, `go vet ./...`
   - Verify no breaking changes

5. **Update Documentation**
   - Update dependency matrix in README (if exists)
   - Note version changes in CHANGELOG
   - Document migration notes (if any)

6. **Push and Create PR**
   ```bash
   git push origin feat/dependency-update
   ```
   - Create PR to main on GitHub
   - Use PR template with consolidated description
   - Include dependency matrix diff in description

## Configuration

### Environment Variables (Optional)

```bash
# Skip tests (not recommended)
export SKIP_TESTS=true

# Use specific branch name
export CONSOLIDATION_BRANCH=chore/update-deps

# Skip static checks
export SKIP_CHECKS=false

# PR template path
export PR_TEMPLATE_PATH=.github/PULL_REQUEST_TEMPLATE.md
```

### Project-Specific Customization

The skill auto-detects project type and runs appropriate checks:
- **Go**: `go test ./...`, `go vet ./...`, `go mod tidy`
- **Node.js**: `npm test`, `npm run lint`
- **Rust**: `cargo test`, `cargo clippy`
- **Python**: `pytest`, `flake8` or `pylint`

## Troubleshooting

### Merge Conflicts

If PRs conflict:
1. Skill attempts automatic resolution with dependency tools (`go mod tidy`, `npm install`, etc.)
2. If manual resolution needed, you'll be prompted to resolve conflicts
3. After fixing: `git add .` and `git commit -m "resolve: merge conflicts"`

### Test Failures

If tests fail after merging:
1. Review test output for specific failures
2. Check if newer versions have breaking changes
3. Either:
   - Update code to match new API (if needed)
   - Pin specific versions (if critical)
   - Split the consolidation into smaller batches

### Missing PR Template

If no template found, skill generates one with:
- All merged PR numbers
- Complete dependency version matrix
- Testing summary
- Standard change type checklist

## Output

After running, you'll get:
- ✅ Summary of merged PRs
- 📊 Dependency version matrix
- 🧪 Test results
- 🔍 Lint/check results
- 🔗 Created PR link and number

## References

- [GitHub Dependabot Documentation](https://docs.github.com/en/code-security/dependabot)
- [Git Merge Workflows](https://git-scm.com/book/en/v2/Git-Branching-Rebasing)
- [PR Template Guide](./references/pr-template-guide.md)
- [Dependency Matrix Format](./references/dependency-matrix.md)
- [Troubleshooting Guide](./references/troubleshooting.md)
