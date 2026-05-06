# Dependency Matrix Format

## Overview

A dependency matrix shows which versions of your project are compatible with which versions of dependencies. This is essential for:
- **Version tracking**: Clear history of dependency updates
- **Compatibility documentation**: What works with what
- **Release notes**: Easy summary of what changed
- **Regression analysis**: Identify when issues were introduced

## Standard Format

### Version Compatibility Matrix

```markdown
## Compatibility

### Version Matrix

| Project | Min Go | Dep A  | Dep B  | Dep C  |
|---------|--------|--------|--------|--------|
| v1.5.0  | 1.25   | v1.7.0 | v2.1.0 | v0.5.2 |
| v1.4.0  | 1.24   | v1.6.0 | v2.0.0 | v0.5.1 |
| v1.3.0  | 1.23   | v1.5.0 | v1.9.0 | v0.5.0 |
```

**Best for**: Language implementations, frameworks with specific dependency requirements

### Package Dependency Matrix (AWS/GCP Example)

```markdown
## AWS SDK Dependencies

| Package                         | Version  | Latest? |
|---------------------------------|----------|---------|
| aws-sdk-go-v2                  | v1.41.7  | ✅      |
| aws-sdk-go-v2/config           | v1.32.17 | ✅      |
| aws-sdk-go-v2/credentials      | v1.19.16 | ✅      |
| aws-sdk-go-v2/service/s3       | v1.101.0 | ✅      |
| aws-sdk-go-v2/service/sqs      | v1.42.27 | ✅      |
| aws-sdk-go-v2/service/sns      | v1.39.17 | ✅      |
| aws/smithy-go                  | v1.25.1  | ✅      |
```

**Best for**: Cloud SDKs, monorepos with many service packages

## Generation

### Go Projects

Extract from `go.mod`:
```bash
# List all dependencies with versions
go list -m all | grep -v "^github.com/nandlabs" | sort
```

### Node.js Projects

Extract from `package-lock.json`:
```bash
# List direct dependencies
npm list --depth=0 --json | jq '.dependencies'
```

### Rust Projects

Extract from `Cargo.lock`:
```bash
# Show dependency tree with versions
cargo tree
```

## When to Include

### Always Include
- Direct dependencies (defined in your package manifest)
- Language minimum version
- Any dependencies with security implications
- Dependencies with known breaking changes

### Optional
- Transitive dependencies (auto-managed)
- Dev dependencies (unless they're substantial)
- Platform-specific dependencies (unless cross-platform is complex)

## Formatting Tips

1. **Use UTF-8 check/x marks**: ✅ ❌ for clarity
2. **Link to changelogs**: `[v1.41.7](https://github.com/aws/aws-sdk-go-v2/releases/tag/v1.41.7)`
3. **Note breaking changes**: Add ⚠️ or `BREAKING` tag
4. **Group related packages**: Keep core, services, and utilities separate
5. **Sort logically**: By package type, not alphabetically

## Update Strategy

### After Consolidating Dependabot PRs

1. **Extract versions**
   ```bash
   go list -m all > /tmp/new-deps.txt
   git diff HEAD~1 go.mod | grep "^+" | grep github
   ```

2. **Generate matrix snippet**
   - Copy version numbers from manifest
   - Format as markdown table
   - Add to README

3. **Document changes**
   - Note major version bumps
   - Flag any breaking changes
   - Link to migration guides

## Example: Before & After

### Before Consolidation
```
| Package | Version |
|---------|---------|
| lib-a   | v1.5.0  |
| lib-b   | v2.0.0  |
```

### After Consolidation
```
| Package | Version  | Prior  | Notes              |
|---------|----------|--------|--------------------|
| lib-a   | v1.6.0   | v1.5.0 | Minor update       |
| lib-b   | v2.1.0   | v2.0.0 | Features added     |
```

## Anti-Patterns

❌ **Don't**: Include every transitive dependency
❌ **Don't**: Show versions from old releases
❌ **Don't**: Create table with 100+ entries (too noisy)
❌ **Don't**: Update matrix only for major releases

✅ **Do**: Focus on direct, significant dependencies
✅ **Do**: Keep current and recent releases only
✅ **Do**: Update matrix with every consolidation
✅ **Do**: Link to documentation for complex packages
