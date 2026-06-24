#!/usr/bin/env bash
# Tag all aifei-go library modules with the same version (lockstep release).
#
# Go multi-module repo convention (no go.mod at repo root):
#   - Every module lives in a subdir <mod>/, its go.mod declares
#     module github.com/crazy-airhead/aifei-go/<mod>, and its version
#     tag is <mod>/vX.Y.Z.
#   - _example/* are not importable libraries and are deliberately excluded.
#
# Usage:
#   ./scripts/tag.sh v0.1.0           # dry-run (print what would be tagged)
#   ./scripts/tag.sh v0.1.0 --do      # create the tags locally
#   ./scripts/tag.sh v0.1.0 --push    # create + push tags to github AND origin
#
# Remotes:
#   github  -> github.com (authoritative for the Go module path / go get)
#   origin  -> cnb.cool   (mirror)

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
  echo "  $0 v0.1.0 --push    # create tags + push to github and origin"
  exit 1
fi

# Validate version format (v<major>.<minor>.<patch>)
if ! [[ "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "ERROR: version must be vX.Y.Z (e.g. v0.1.0)"
  exit 2
fi

# ---------------------------------------------------------------------------
# Module list — every importable library module.
# Order is cosmetic; subdir tags carry their own <mod>/ prefix, so there is no
# dependency ordering requirement between them.
# ---------------------------------------------------------------------------
MODULES=(
  aifei         # github.com/crazy-airhead/aifei-go/aifei    (./aifei/go.mod)
  db            # github.com/crazy-airhead/aifei-go/db       (./db/go.mod)
  enjoy         # github.com/crazy-airhead/aifei-go/enjoy    (./enjoy/go.mod)
  generator     # github.com/crazy-airhead/aifei-go/generator (./generator/go.mod)
  go-http       # github.com/crazy-airhead/aifei-go/go-http  (./go-http/go.mod)
  json          # github.com/crazy-airhead/aifei-go/json     (./json/go.mod)
  log           # github.com/crazy-airhead/aifei-go/log      (./log/go.mod)
  server        # github.com/crazy-airhead/aifei-go/server   (./server/go.mod)
)

# Remotes to push to (github publishes to the Go module proxy).
REMOTES=(github origin)

# ---------------------------------------------------------------------------
# Resolve to root of the git repo
# ---------------------------------------------------------------------------
cd "$(git rev-parse --show-toplevel)"

echo "Tag   : $VERSION"
echo "Branch: $(git branch --show-current)"
echo "github: $(git remote get-url github 2>/dev/null || echo '(missing remote!)')"
echo "origin: $(git remote get-url origin 2>/dev/null || echo '(missing remote!)')"
echo ""

# ---------------------------------------------------------------------------
# Sanity: every module dir must actually contain a go.mod
# ---------------------------------------------------------------------------
for mod in "${MODULES[@]}"; do
  if [[ ! -f "$mod/go.mod" ]]; then
    echo "ERROR: $mod/go.mod not found (module list is out of date?)"
    exit 3
  fi
done

# ---------------------------------------------------------------------------
# Check that working tree is clean (only when creating tags)
# ---------------------------------------------------------------------------
if [[ "$DO" == "--do" || "$DO" == "--push" ]]; then
  if ! git diff-index --quiet HEAD --; then
    echo "ERROR: working tree is dirty. Commit or stash changes first."
    exit 4
  fi
fi

# ---------------------------------------------------------------------------
# Create tags
# ---------------------------------------------------------------------------
echo "=== Modules to tag ==="
CREATED_ANY=0
for mod in "${MODULES[@]}"; do
  TAG="$mod/$VERSION"
  LABEL="github.com/crazy-airhead/aifei-go/$mod"

  printf "  %-55s -> %s\n" "$LABEL" "$TAG"

  if [[ "$DO" == "--do" || "$DO" == "--push" ]]; then
    if git rev-parse "$TAG" >/dev/null 2>&1; then
      echo "    (skip: already exists)"
      continue
    fi

    git tag -a "$TAG" -m "$LABEL $VERSION"
    echo "    (created)"
    CREATED_ANY=1
  fi
done
echo ""

# ---------------------------------------------------------------------------
# Push — send all refs in a single git push per remote (avoids half-pushed
# state if one ref were to fail mid-loop).
# ---------------------------------------------------------------------------
if [[ "$DO" == "--push" ]]; then
  REFS=()
  for mod in "${MODULES[@]}"; do
    REFS+=("$mod/$VERSION")
  done

  echo "=== Pushing tags ==="
  FAILED=0
  for remote in "${REMOTES[@]}"; do
    if ! git remote get-url "$remote" >/dev/null 2>&1; then
      echo "  $remote: (missing remote, skipped)"
      FAILED=1
      continue
    fi
    printf "  -> %s  (%s)\n" "$remote" "$(git remote get-url "$remote")"
    if git push "$remote" "${REFS[@]}"; then
      echo "     (ok)"
    else
      echo "     (FAILED)"
      FAILED=1
    fi
  done

  echo ""
  if [[ "$FAILED" == "1" ]]; then
    echo "WARNING: at least one remote failed. Local tags were created; retry the push manually."
    exit 5
  fi
  echo "Done. All tags pushed to: ${REMOTES[*]}"
elif [[ "$DO" == "--do" ]]; then
  echo "Done. Tags created locally."
  echo "Run './scripts/tag.sh $VERSION --push' to push, or push manually."
else
  echo "Dry run. Add --do to create tags, --push to create + push."
fi
