# Troubleshooting Guide

## Common Issues and Solutions

### Merge Conflicts in go.mod / package.json

**Problem**: Multiple dependabot PRs modify the same dependency file.

**Solutions**:
1. **Automatic Resolution** (Recommended)
   ```bash
   go mod tidy        # Go projects
   npm install        # Node.js projects
   cargo update       # Rust projects
   ```

2. **Manual Resolution**
   - Use your editor's merge conflict UI
   - Review each conflict marker
   - Accept the highest version from either side
   - Run `go mod tidy` after resolving

3. **Resolve via Skill**
   - Skill detects conflicts and attempts auto-resolution
   - If manual intervention needed, you'll be prompted

### Test Failures After Merge

**Problem**: New versions introduce breaking changes.

**Diagnosis**:
1. Check the failing test output
2. Identify which dependency version caused the issue
3. Review the dependency's changelog

**Solutions**:
1. **Update Code** (Preferred)
   - Update code to use new API
   - Run tests to verify fix
   - Document migration in PR

2. **Pin Version** (Temporary)
   - Keep old version with `// indirect` comment
   - Create issue for future migration
   - Document reason in commit

3. **Split Consolidation**
   - Separate problematic PR into own branch
   - Test independently before consolidating
   - Coordinate with team on version strategy

### Merge Strategy Conflicts

**Problem**: Dependency version conflicts between PRs (PR A needs v1.5, PR B needs v1.4).

**Solutions**:
1. **Use Latest**: Skill defaults to latest compatible version
2. **Use Dependency Lock**: Most modern tools handle transitive deps automatically
3. **Review PR Descriptions**: Check if there are version constraints noted

### "Base branch was modified" Error

**Problem**: Main branch changed while working on consolidation branch.

**Solution**:
```bash
git fetch origin main
git rebase origin/main feat/dependency-update
# OR
git merge origin/main
# Handle any conflicts, then:
git push -f origin feat/dependency-update
```

### Static Check Failures

**Problem**: Linter or formatter complains about changes.

**Solutions by Project Type**:

**Go**:
```bash
go fmt ./...           # Auto-format
goimports -w .         # Fix imports
golangci-lint run      # Check all rules
```

**Node.js**:
```bash
npm run format         # Auto-format (if available)
npm run lint -- --fix # Fix linter issues
```

**Rust**:
```bash
cargo fmt
cargo clippy --fix
```

### Large Number of Conflicts

**Problem**: Too many conflicting PRs to merge sequentially.

**Solutions**:
1. **Batch smaller groups**: Create multiple consolidation branches (5-7 PRs each)
2. **Rebase strategy**: Instead of merge, try `git rebase`
3. **Manual conflict resolution**: Have team help resolve major conflicts
4. **Contact upstream**: If many conflicts, ask dependabot to regenerate PRs

### Missing PR Template

**Problem**: `.github/PULL_REQUEST_TEMPLATE.md` not found.

**Solution**: 
- Skill auto-generates template based on:
  - Project type (Go, Node, Rust, Python, etc.)
  - Dependency changes
  - Typical security/breaking change patterns
- You can customize the generated template before submission

## Preventive Measures

### Reduce Merge Conflicts

1. **Merge frequently**: Don't let PRs age
2. **Use dependency lock files**: Ensures reproducible versions
3. **Configure dependabot groups**: Group similar updates

### Improve Test Coverage

1. **Run tests before consolidating**
2. **Test with multiple Go versions** (if applicable)
3. **Test with multiple Node versions** (if applicable)

### Version Strategy

1. **Document version policy**: Which updates are safe?
2. **Communicate major changes**: Notify team of breaking updates
3. **Schedule consolidation**: Weekly or bi-weekly, not ad-hoc

## Getting Help

If issues persist:
1. **Review detailed error output** from failed checks
2. **Check dependency changelogs** for breaking changes
3. **Ask in #dependencies Slack channel** (if available)
4. **Open an issue** on project repository with:
   - PR numbers involved
   - Error messages
   - Steps to reproduce
   - Git history output
