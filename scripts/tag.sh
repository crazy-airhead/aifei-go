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
# Order matters for dependency resolution during go.mod update:
# leaf modules first, then modules that depend on them.
# ---------------------------------------------------------------------------
MODULES=(
  aifei         # github.com/crazy-airhead/aifei-go/aifei    (./aifei/go.mod)
  config        # github.com/crazy-airhead/aifei-go/config   (./config/go.mod)
  db            # github.com/crazy-airhead/aifei-go/db       (./db/go.mod)
  enjoy         # github.com/crazy-airhead/aifei-go/enjoy    (./enjoy/go.mod)
  json          # github.com/crazy-airhead/aifei-go/json     (./json/go.mod)
  log           # github.com/crazy-airhead/aifei-go/log      (./log/go.mod)
  nami          # github.com/crazy-airhead/aifei-go/nami     (./nami/go.mod)
  go-http       # github.com/crazy-airhead/aifei-go/go-http  (./go-http/go.mod)   → aifei
  generator     # github.com/crazy-airhead/aifei-go/generator (./generator/go.mod) → db, enjoy
  server        # github.com/crazy-airhead/aifei-go/server   (./server/go.mod)     → aifei, go-http
  plugins/nacos   # github.com/crazy-airhead/aifei-go/plugins/nacos   (./plugins/nacos/go.mod)   → aifei, config, log, nami
  plugins/storage # github.com/crazy-airhead/aifei-go/plugins/storage (./plugins/storage/go.mod) → aifei, config, log
  plugins/cache   # github.com/crazy-airhead/aifei-go/plugins/cache   (./plugins/cache/go.mod)   → aifei, config, log
  plugins/kafka   # github.com/crazy-airhead/aifei-go/plugins/kafka   (./plugins/kafka/go.mod)   → aifei, config, log
  plugins/swagger # github.com/crazy-airhead/aifei-go/plugins/swagger (./plugins/swagger/go.mod) → aifei, config, log
)

# Internal dependency map: "module:deps" pairs (compatible with bash 3.2).
# Format: "<module>:<space-separated list of repo modules it depends on>"
# Only modules that depend on other modules in this repo need entries here.
MODULE_DEPS=(
  "go-http:aifei"
  "generator:db enjoy"
  "server:aifei go-http"
  "plugins/nacos:aifei config log nami"
  "plugins/storage:aifei config log"
  "plugins/cache:aifei config log"
  "plugins/kafka:aifei config log"
  "plugins/swagger:aifei config log"
)

# Remotes to push to (github publishes to the Go module proxy).
REMOTES=(github origin)

# ---------------------------------------------------------------------------
# Resolve to root of the git repo
# ---------------------------------------------------------------------------
cd "$(git rev-parse --show-toplevel)"

echo "Tag   : $VERSION"
BRANCH="$(git branch --show-current)"
echo "Branch: $BRANCH"
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
# Update internal dependency versions in go.mod files
# ---------------------------------------------------------------------------
if [[ "$DO" == "--do" || "$DO" == "--push" ]]; then
  echo "=== Updating internal dependency versions to $VERSION ==="
  UPDATED_ANY=0

  for entry in "${MODULE_DEPS[@]}"; do
    mod="${entry%%:*}"
    DEPS="${entry#*:}"
    MOD_FILE="$mod/go.mod"
    MODIFIED=0

    for dep in $DEPS; do
      DEP_PATH="github.com/crazy-airhead/aifei-go/${dep}"

      # Update require line: replace old version with new version
      if grep -q "${DEP_PATH} v" "$MOD_FILE"; then
        sed -i '' "s|\\(${DEP_PATH}\\) v[0-9][0-9.]*|\\1 ${VERSION}|g" "$MOD_FILE"
        echo "  ${mod}/go.mod: ${dep} → ${VERSION}"
        MODIFIED=1
      else
        echo "  WARNING: ${mod}/go.mod does not require ${DEP_PATH} (skip)"
      fi
    done

    if [[ "$MODIFIED" == "1" ]]; then
      # Run go mod tidy to sync go.sum (go.work resolves local modules)
      echo "  → go mod tidy ${mod}/"
      if ! (cd "$mod" && go mod tidy); then
        echo "ERROR: go mod tidy failed in ${mod}/"
        exit 5
      fi
      UPDATED_ANY=1
    fi
  done

  echo ""

  # Commit the go.mod / go.sum changes so the tag points to a consistent state
  if [[ "$UPDATED_ANY" == "1" ]]; then
    echo "=== Committing dependency updates ==="
    git add -A
    if git diff-index --quiet HEAD --; then
      echo "  (no changes to commit)"
    else
      git commit -m "chore: bump internal deps to ${VERSION}"
      echo "  committed: bump internal deps to ${VERSION}"
    fi
    echo ""
  else
    echo "  (no dependency updates needed)"
    echo ""
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
# Push — push the commit first, then tags. Both go in a single push per remote
# to avoid half-pushed state.
# ---------------------------------------------------------------------------
if [[ "$DO" == "--push" ]]; then
  REFS=()
  for mod in "${MODULES[@]}"; do
    REFS+=("$mod/$VERSION")
  done

  echo "=== Pushing branch + tags ==="
  FAILED=0
  for remote in "${REMOTES[@]}"; do
    if ! git remote get-url "$remote" >/dev/null 2>&1; then
      echo "  $remote: (missing remote, skipped)"
      FAILED=1
      continue
    fi
    printf "  -> %s  (%s)\n" "$remote" "$(git remote get-url "$remote")"
    # Push branch HEAD + all tags in one push
    if git push "$remote" "HEAD:${BRANCH}" "${REFS[@]}"; then
      echo "     (ok)"
    else
      echo "     (FAILED)"
      FAILED=1
    fi
  done

  echo ""
  if [[ "$FAILED" == "1" ]]; then
    echo "WARNING: at least one remote failed. Local tags were created; retry the push manually."
    exit 6
  fi
  echo "Done. All tags pushed to: ${REMOTES[*]}"
elif [[ "$DO" == "--do" ]]; then
  echo "Done. Tags created locally."
  echo "Run './scripts/tag.sh $VERSION --push' to push, or push manually."
else
  echo "Dry run. Add --do to create tags, --push to create + push."
fi
