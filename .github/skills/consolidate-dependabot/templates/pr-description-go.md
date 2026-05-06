## Description

Consolidates all pending dependabot pull requests into a single comprehensive update. This ensures all dependency changes are tested together and provides a single cohesive point for review.

### Related Issues

Addresses dependabot PRs: {{PR_NUMBERS}}

## Type of Change

- [x] 🔨 Build / CI changes

## Changes Made

{{DEPENDENCY_CHANGES}}

Updated README.md with comprehensive dependency matrix for visibility into all transitive and direct dependencies.

Merged PRs: {{MERGED_PR_LINKS}}

## Testing

- [x] All existing tests pass (`go test ./...`)
- [x] I have tested this locally

### Test Output

Dependencies validated with `go mod tidy` and verified across all package imports.

## Checklist

- [x] My code follows the project's coding style and conventions
- [x] I have performed a self-review of my code
- [x] I have updated the documentation accordingly
- [x] My changes generate no new warnings or errors
- [x] I have read the [CONTRIBUTING](../CONTRIBUTING.md) guide

## Additional Context

This consolidation approach ensures all dependency updates are tested together before merging to main. The `feat/dependency-update` branch serves as the authoritative source for pending dependency changes and can be referenced for future bulk updates.

All updates are to patch and minor versions. No breaking changes detected.
