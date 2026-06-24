#!/usr/bin/env bash
# Tag all aifei-go modules with the same version.
#
# Go multi-module repo convention (no go.mod at repo root):
#   - ALL modules are in subdirs  -> <subdir>/vX.Y.Z
#
# Usage:
#   ./scripts/tag.sh v0.1.0           # dry-run (print commands only)
#   ./scripts/tag.sh v0.1.0 --do      # actually create tags
#   ./scripts/tag.sh v0.1.0 --push    # create + push tags to origin

set -euo pipefail

# ---------------------------------------------------------------------------
# Parse args
# ---------------------------------------------------------------------------
VERSION="${1:-}"
DO="${2:-}"          # --do | --push

if [[ -z "$VERSION" ]]; then
  echo "Usage: $0 <version> [--do|--push]"
  echo ""
  echo "Examples:"
  echo "  $0 v0.1.0           # dry-run"
  echo "  $0 v0.1.0 --do      # create local tags"
  echo "  $0 v0.1.0 --push    # create tags + push to origin"
  exit 1
fi

# Validate version format (v<major>.<minor>.<patch>)
if ! [[ "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "ERROR: version must be vX.Y.Z (e.g. v0.1.0)"
  exit 2
fi

# ---------------------------------------------------------------------------
# Module list — order matters: root first so the root tag is the tree root
# ---------------------------------------------------------------------------
# ALL modules live in subdirectories — there is NO go.mod at the repo root.
MODULES=(
  aifei         # github.com/crazy-airhead/aifei-go        (./aifei/go.mod)
  db            # github.com/crazy-airhead/aifei-go/db     (./db/go.mod)
  enjoy         # github.com/crazy-airhead/aifei-go/enjoy  (./enjoy/go.mod)
  generator
  go-http
  json
  log
  server
)

# ---------------------------------------------------------------------------
# Resolve to root of the git repo
# ---------------------------------------------------------------------------
cd "$(git rev-parse --show-toplevel)"

echo "Repo  : $(git remote get-url origin 2>/dev/null || echo '(no remote)')"
echo "Branch: $(git branch --show-current)"
echo "Tag   : $VERSION"
echo ""

# ---------------------------------------------------------------------------
# Check that working tree is clean (only when creating tags)
# ---------------------------------------------------------------------------
if [[ "$DO" == "--do" || "$DO" == "--push" ]]; then
  if ! git diff-index --quiet HEAD --; then
    echo "ERROR: working tree is dirty. Commit or stash changes first."
    exit 3
  fi
fi

# ---------------------------------------------------------------------------
# Create tags
# ---------------------------------------------------------------------------
echo "=== Modules to tag ==="
for mod in "${MODULES[@]}"; do
  TAG="$mod/$VERSION"
  LABEL="github.com/crazy-airhead/aifei-go/$mod"

  printf "  %-55s -> %s\n" "$LABEL" "$TAG"

  if [[ "$DO" == "--do" || "$DO" == "--push" ]]; then
    # Check if tag already exists
    if git rev-parse "$TAG" >/dev/null 2>&1; then
      echo "    (skip: already exists)"
      continue
    fi

    git tag -a "$TAG" -m "$LABEL $VERSION"
    echo "    (created)"
  fi
done

echo ""

# ---------------------------------------------------------------------------
# Push
# ---------------------------------------------------------------------------
if [[ "$DO" == "--push" ]]; then
  echo "=== Pushing tags ==="
  for mod in "${MODULES[@]}"; do
    git push origin "$mod/$VERSION"
  done
  echo ""
  echo "Done. All tags pushed."
elif [[ "$DO" == "--do" ]]; then
  echo "Done. Tags created locally."
  echo "Run 'git push origin --tags' to push, or re-run with --push."
else
  echo "Dry run. Add --do to create tags, --push to create + push."
fi
