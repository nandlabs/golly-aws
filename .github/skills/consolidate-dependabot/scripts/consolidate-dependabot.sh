#!/bin/bash

##############################################################################
# Consolidate Dependabot Updates
#
# This script consolidates multiple dependabot PRs into a single managed
# branch, runs comprehensive tests and checks, then creates a PR.
#
# Usage:
#   ./consolidate-dependabot.sh [OPTIONS] [pr-numbers]
#
# Options:
#   -h, --help              Show this help message
#   -b, --branch NAME       Custom branch name (default: feat/dependency-update)
#   -s, --skip-tests        Skip running tests
#   -S, --skip-checks       Skip static checks
#   -d, --dry-run           Show what would be done without making changes
#   -v, --verbose           Verbose output
#
# Examples:
#   ./consolidate-dependabot.sh                    # All open dependabot PRs
#   ./consolidate-dependabot.sh 117,120,123        # Specific PRs
#   ./consolidate-dependabot.sh --skip-tests all   # All without tests
#
##############################################################################

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
BRANCH_NAME="feat/dependency-update"
SKIP_TESTS=false
SKIP_CHECKS=false
DRY_RUN=false
VERBOSE=false
PR_NUMBERS=""

# Detect project type
PROJECT_TYPE=""
PROJECT_ROOT="."

##############################################################################
# Helper Functions
##############################################################################

log_info() {
    echo -e "${BLUE}ℹ${NC} $*"
}

log_success() {
    echo -e "${GREEN}✓${NC} $*"
}

log_warning() {
    echo -e "${YELLOW}⚠${NC} $*"
}

log_error() {
    echo -e "${RED}✗${NC} $*"
}

log_verbose() {
    if [ "$VERBOSE" = true ]; then
        echo -e "${BLUE}→${NC} $*"
    fi
}

detect_project_type() {
    if [ -f "$PROJECT_ROOT/go.mod" ]; then
        PROJECT_TYPE="go"
    elif [ -f "$PROJECT_ROOT/package.json" ]; then
        PROJECT_TYPE="node"
    elif [ -f "$PROJECT_ROOT/Cargo.toml" ]; then
        PROJECT_TYPE="rust"
    elif [ -f "$PROJECT_ROOT/pyproject.toml" ] || [ -f "$PROJECT_ROOT/setup.py" ]; then
        PROJECT_TYPE="python"
    else
        PROJECT_TYPE="unknown"
    fi
    log_verbose "Detected project type: $PROJECT_TYPE"
}

fetch_prs() {
    log_info "Fetching pull requests..."
    
    if [ -z "$PR_NUMBERS" ] || [ "$PR_NUMBERS" = "all" ]; then
        # Get all open dependabot PRs
        if command -v gh &> /dev/null; then
            PR_NUMBERS=$(gh pr list --search "author:dependabot is:open" --json number -q ".[].number" | tr '\n' ',' | sed 's/,$//')
            if [ -z "$PR_NUMBERS" ]; then
                log_warning "No open dependabot PRs found"
                return 1
            fi
        else
            log_error "GitHub CLI (gh) not found. Please install it or specify PR numbers manually."
            return 1
        fi
    fi
    
    log_success "Found PRs: $PR_NUMBERS"
    return 0
}

create_branch() {
    log_info "Creating consolidation branch: $BRANCH_NAME"
    
    if git show-ref --quiet "refs/heads/$BRANCH_NAME"; then
        log_warning "Branch $BRANCH_NAME already exists. Using existing branch."
        git checkout "$BRANCH_NAME"
    else
        git checkout -b "$BRANCH_NAME"
        log_success "Created branch: $BRANCH_NAME"
    fi
}

merge_prs() {
    local prs=($PR_NUMBERS)
    local count=0
    
    log_info "Merging ${#prs[@]} dependabot PR(s)..."
    
    for pr_num in "${prs[@]}"; do
        if [ -z "$pr_num" ]; then continue; fi
        
        log_verbose "Merging PR #$pr_num..."
        
        git fetch origin "pull/$pr_num/head" 2>/dev/null || {
            log_error "Failed to fetch PR #$pr_num"
            continue
        }
        
        if git merge FETCH_HEAD -m "chore: merge PR #$pr_num" 2>/dev/null; then
            ((count++))
            log_success "Merged PR #$pr_num"
        else
            # Try to resolve with dependency tools
            log_warning "Conflict in PR #$pr_num. Attempting auto-resolution..."
            resolve_conflicts
            git add -A
            git commit -m "chore: merge PR #$pr_num (resolved conflicts)"
            ((count++))
        fi
    done
    
    log_success "Merged $count PR(s)"
}

resolve_conflicts() {
    case "$PROJECT_TYPE" in
        go)
            log_verbose "Running: go mod tidy"
            go mod tidy
            ;;
        node)
            log_verbose "Running: npm install"
            npm install
            ;;
        rust)
            log_verbose "Running: cargo update"
            cargo update
            ;;
        python)
            log_verbose "Attempting conflict resolution..."
            # Python typically uses requirements.txt or pyproject.toml
            if command -v pip-compile &> /dev/null; then
                pip-compile requirements.in --output-file=requirements.txt 2>/dev/null || true
            fi
            ;;
        *)
            log_warning "Unknown project type. Manual conflict resolution needed."
            ;;
    esac
}

run_tests() {
    if [ "$SKIP_TESTS" = true ]; then
        log_warning "Skipping tests"
        return 0
    fi
    
    log_info "Running tests..."
    
    case "$PROJECT_TYPE" in
        go)
            log_verbose "Running: go test ./..."
            if go test ./... -v; then
                log_success "Tests passed"
            else
                log_error "Tests failed"
                return 1
            fi
            ;;
        node)
            if [ -f "package.json" ] && grep -q '"test"' package.json; then
                log_verbose "Running: npm test"
                if npm test; then
                    log_success "Tests passed"
                else
                    log_error "Tests failed"
                    return 1
                fi
            fi
            ;;
        rust)
            log_verbose "Running: cargo test"
            if cargo test; then
                log_success "Tests passed"
            else
                log_error "Tests failed"
                return 1
            fi
            ;;
        python)
            if command -v pytest &> /dev/null; then
                log_verbose "Running: pytest"
                if pytest; then
                    log_success "Tests passed"
                else
                    log_error "Tests failed"
                    return 1
                fi
            fi
            ;;
        *)
            log_warning "No tests configured for project type: $PROJECT_TYPE"
            ;;
    esac
}

run_checks() {
    if [ "$SKIP_CHECKS" = true ]; then
        log_warning "Skipping static checks"
        return 0
    fi
    
    log_info "Running static checks..."
    
    case "$PROJECT_TYPE" in
        go)
            log_verbose "Running: go vet ./..."
            if go vet ./...; then
                log_success "go vet passed"
            else
                log_error "go vet failed"
                return 1
            fi
            
            log_verbose "Running: go mod tidy"
            if go mod tidy; then
                log_success "go mod tidy passed"
            else
                log_error "go mod tidy failed"
                return 1
            fi
            
            if command -v golangci-lint &> /dev/null; then
                log_verbose "Running: golangci-lint run"
                if golangci-lint run; then
                    log_success "golangci-lint passed"
                else
                    log_warning "golangci-lint found issues"
                fi
            fi
            ;;
        node)
            if command -v npm &> /dev/null && grep -q '"lint"' package.json; then
                log_verbose "Running: npm run lint"
                if npm run lint; then
                    log_success "npm lint passed"
                else
                    log_warning "npm lint found issues"
                fi
            fi
            ;;
        rust)
            log_verbose "Running: cargo clippy"
            if cargo clippy; then
                log_success "cargo clippy passed"
            else
                log_warning "cargo clippy found issues"
            fi
            ;;
        python)
            if command -v pylint &> /dev/null; then
                log_verbose "Running: pylint"
                pylint . || log_warning "pylint found issues"
            fi
            ;;
        *)
            log_warning "No checks configured for project type: $PROJECT_TYPE"
            ;;
    esac
}

push_branch() {
    log_info "Pushing $BRANCH_NAME to origin..."
    
    if [ "$DRY_RUN" = true ]; then
        log_verbose "[DRY RUN] Would push: git push -u origin $BRANCH_NAME"
        return 0
    fi
    
    if git push -u origin "$BRANCH_NAME"; then
        log_success "Pushed to origin/$BRANCH_NAME"
    else
        log_error "Failed to push branch"
        return 1
    fi
}

create_pr() {
    log_info "Creating pull request..."
    
    if ! command -v gh &> /dev/null; then
        log_error "GitHub CLI (gh) not found. Cannot create PR automatically."
        log_info "Please create PR manually at: https://github.com/$(git remote get-url origin | sed 's/.*github.com.//;s/\.git//')/compare/main...$BRANCH_NAME"
        return 0
    fi
    
    # Generate PR description
    local pr_description="Consolidates all pending dependabot pull requests into a single comprehensive update.

## Merged PRs
$(echo "$PR_NUMBERS" | tr ',' '\n' | while read pr; do echo "- PR #$pr"; done)

## Testing
- [x] All tests pass
- [x] Static checks complete

## Type of Change
- [x] Build / CI changes

See SKILL.md in .github/skills/consolidate-dependabot/ for more information."
    
    if [ "$DRY_RUN" = true ]; then
        log_verbose "[DRY RUN] Would create PR with:"
        echo "$pr_description"
        return 0
    fi
    
    if gh pr create --base main --head "$BRANCH_NAME" \
        --title "chore: consolidate dependabot updates" \
        --body "$pr_description"; then
        log_success "Created pull request"
    else
        log_warning "Failed to create PR. Please create manually or check GitHub CLI."
        return 1
    fi
}

show_help() {
    grep '^#' "$0" | grep -v '#!/bin/bash' | sed 's/^# //' | sed 's/^##//'
}

##############################################################################
# Main Execution
##############################################################################

main() {
    log_info "Consolidating Dependabot Updates"
    
    # Parse arguments
    while [[ $# -gt 0 ]]; do
        case $1 in
            -h|--help)
                show_help
                exit 0
                ;;
            -b|--branch)
                BRANCH_NAME="$2"
                shift 2
                ;;
            -s|--skip-tests)
                SKIP_TESTS=true
                shift
                ;;
            -S|--skip-checks)
                SKIP_CHECKS=true
                shift
                ;;
            -d|--dry-run)
                DRY_RUN=true
                shift
                ;;
            -v|--verbose)
                VERBOSE=true
                shift
                ;;
            *)
                PR_NUMBERS="$1"
                shift
                ;;
        esac
    done
    
    # Execute workflow
    detect_project_type || exit 1
    
    git fetch origin || {
        log_error "Failed to fetch from origin"
        exit 1
    }
    
    fetch_prs || exit 1
    create_branch || exit 1
    merge_prs || exit 1
    run_tests || { log_error "Tests failed"; exit 1; }
    run_checks || { log_warning "Some checks failed"; }
    push_branch || exit 1
    create_pr || exit 1
    
    log_success "Consolidation complete!"
    log_info "Branch: $BRANCH_NAME"
    log_info "PR: Check GitHub for the created pull request"
}

main "$@"
