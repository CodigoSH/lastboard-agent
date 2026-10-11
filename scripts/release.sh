#!/bin/bash

# ==========================================
# 🚀 Unified Release Script
# Handles: Stable, Beta, and RC releases
#
# Usage: ./scripts/release.sh [stable|beta|rc] [X.Y.Z] ["release title"]
#                                              [--dry-run] [--yes] [--skip-checks]
#
# The title is the one-line summary of the release. It heads the notes — it is
# not a changelog entry. Passing it as an argument is what keeps it from falling
# back to "minor updates".
#
# There is no version file to rewrite: the version is the tag, and the builds
# stamp it into the binary with -X main.version. So the current version is read
# from the last tag, and nothing is committed before the notes.
#
# X.Y.Z overrides the computed version. Needed for stable-from-stable, where the
# computed one is the version already tagged.
#
# --dry-run prints the version it would produce, checks every file it would
# rewrite, renders the changelog and lists the git commands — without touching
# a single file. Use it to inspect a release before making it. It also states
# where a real run would stop to ask for confirmation.
# ==========================================

# Fail loudly: an unchecked error here means a tag pushed against a broken tree.
set -euo pipefail

# --- Arguments ------------------------------------------------------------

TYPE=""
TITLE=""
FORCED_VERSION=""
DRY_RUN=0
SKIP_CHECKS=0
ASSUME_YES=0

for arg in "$@"; do
    case "$arg" in
        stable|beta|rc)  TYPE="$arg" ;;
        --dry-run)       DRY_RUN=1 ;;
        --skip-checks)   SKIP_CHECKS=1 ;;
        --yes|-y)        ASSUME_YES=1 ;;
        --*)             echo "❌ Unknown option: $arg"; exit 1 ;;
        # A bare X.Y.Z is the target version, not a title. This is the
        # stable-from-stable case, where the computed version is the one already
        # tagged and the real one has to be stated.
        [0-9]*.[0-9]*.[0-9]*) FORCED_VERSION="$arg" ;;
        *)               TITLE="$arg" ;;
    esac
done

if [[ "$TYPE" != "stable" && "$TYPE" != "beta" && "$TYPE" != "rc" ]]; then
    echo "Usage: ./scripts/release.sh [stable|beta|rc] [X.Y.Z] [\"release title\"] [options]"
    echo "  stable : Promotes current version to stable (removes suffixes)"
    echo "  beta   : Bumps Beta version (creates new patch-Beta.01 if current is stable)"
    echo "  rc     : Bumps RC version"
    echo
    echo "  X.Y.Z          Target version, for when the computed one is not the one you want"
    echo "                 (stable-from-stable: 1.6.2 is already tagged, so pass 1.6.3)"
    echo "  --dry-run      Show what would happen, change nothing"
    echo "  --yes, -y      Don't ask for confirmation (required to run stable without a terminal)"
    echo "  --skip-checks  Skip the test/build preflight (emergencies only)"
    exit 1
fi

# A stable moves the Docker `latest` tag, which every compose file in the
# documentation pulls, so it asks before pushing. Without a terminal that question
# cannot be answered — fail here rather than after two minutes of preflight,
# which is what used to happen.
if [ "$TYPE" = "stable" ] && [ "$DRY_RUN" = "0" ] && [ "$ASSUME_YES" = "0" ] && [ ! -t 0 ]; then
    echo "❌ 'stable' asks for confirmation and there is no terminal to ask on."
    echo "   Re-run with --yes to confirm up front."
    exit 1
fi

# Configuration
# The other products of the house. Their names never reach what a release
# publishes — its title, its notes, the changelog: what a project takes from
# another becomes its own, and is described by what it does. Lastboard is not
# on the list: this is its agent, and saying so is the point.
OTHER_PRODUCTS=(LastHelp LastGYM LastDAV)

RELEASE_BRANCH="main"
# Pinned, and pinned to the same value .forgejo/workflows/tests.yml uses: a
# checker that is not the one CI runs proves nothing about CI.
# A pseudo-version, deliberately: the last tagged release reads the compiler's export
# data up to format 4 and Go 1.27.2 writes format 5, so every package that imports the
# standard library fails to load. The commit is exact and immutable on the module proxy;
# it goes back to a tagged release the day one reads format 5.
STATICCHECK_VERSION="v0.7.0-0.dev.0.20261009230814-452d5bb86b45"

# The vulnerability scan is a preflight step because of where it fails: in CI it turns
# the repository red after the tag is public, here it stops the release before anything
# is written. Pinned to the version tests.yml uses; the tool is pinned, the advisory
# database it reads is not, and that is the point.
GOVULNCHECK_VERSION="v1.9.0"

die() { echo "❌ $1" >&2; exit 1; }

# Runs a command, or prints it when doing a dry run.
run() {
    if [ "$DRY_RUN" = "1" ]; then
        echo "   [dry-run] $*"
    else
        "$@"
    fi
}

if [ "$DRY_RUN" = "1" ]; then
    echo "🧪 DRY RUN — nothing will be written, committed or pushed."
    echo
fi

# --- 0. Preconditions ------------------------------------------------------

# A release must be reproducible from what is published, so the tree has to be
# clean and level with the remote before anything is rewritten. The old script
# swept whatever was lying around into the release commit with `git add .`.
echo "🔍 Checking the repository..."

[ -f go.mod ] || die "go.mod not found — run this from the project root."
git rev-parse --git-dir >/dev/null 2>&1 || die "Not a git repository."

CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)
[ "$CURRENT_BRANCH" = "$RELEASE_BRANCH" ] || \
    die "On branch '$CURRENT_BRANCH'. Releases are cut from '$RELEASE_BRANCH'."

if [ -n "$(git status --porcelain)" ]; then
    echo "   Uncommitted changes:"
    git status --short | sed 's/^/     /'
    # A dry run is for inspecting a release before making it, so it reports the
    # dirty tree and carries on; a real run stops.
    [ "$DRY_RUN" = "1" ] || \
        die "Commit or stash them first — a release is cut from what is committed, and nothing else."
    echo "   ⚠️  A real run would stop here."
    TREE_DIRTY=1
fi
TREE_DIRTY=${TREE_DIRTY:-0}

git fetch origin "$RELEASE_BRANCH" --quiet 2>/dev/null || \
    echo "   ⚠️  Could not reach origin; skipping the sync check."
if git rev-parse --verify --quiet "origin/$RELEASE_BRANCH" >/dev/null; then
    BEHIND=$(git rev-list --count "HEAD..origin/$RELEASE_BRANCH")
    [ "$BEHIND" = "0" ] || \
        die "Local $RELEASE_BRANCH is $BEHIND commit(s) behind origin. Pull first."
fi

if [ "$TREE_DIRTY" = "0" ]; then
    echo "   ✅ On $RELEASE_BRANCH, clean and up to date."
else
    echo "   ✅ On $RELEASE_BRANCH, up to date with origin."
fi

# --- 1. Preflight ----------------------------------------------------------

# Tag and push happen further down and cannot be taken back cheaply, so the
# suite runs *before* anything is written rather than by hand beforehand.
if [ "$SKIP_CHECKS" = "1" ]; then
    echo "⏭️  Preflight skipped (--skip-checks)."
else
    echo "🧪 Preflight..."

    UNFORMATTED=$(gofmt -l . 2>/dev/null | grep -v '^vendor/' || true)
    [ -z "$UNFORMATTED" ] || die "gofmt reports unformatted files:"$'\n'"$UNFORMATTED"
    echo "   ✅ gofmt"

    go vet ./... || die "go vet failed."
    echo "   ✅ go vet"

    # The same analysis the Tests workflow runs, and pinned to the same
    # version it uses — a release that would turn that job red should not be
    # taggable. Dead code is the usual reason: it compiles, vets and tests
    # perfectly, and only this catches it.
    #
    # `go run` rather than an install: no tool has to be present on the machine
    # cutting the release, and the version cannot drift from CI's by accident.
    go run honnef.co/go/tools/cmd/staticcheck@"$STATICCHECK_VERSION" ./... || \
        die "staticcheck failed. Run 'go run honnef.co/go/tools/cmd/staticcheck@$STATICCHECK_VERSION ./...' to see why."
    echo "   ✅ staticcheck"

    # It exits non-zero only for what this code reaches, and the usual cause is the Go
    # toolchain being a patch behind.
    go run golang.org/x/vuln/cmd/govulncheck@"$GOVULNCHECK_VERSION" ./... >/dev/null || \
        die "govulncheck failed. Run 'go run golang.org/x/vuln/cmd/govulncheck@$GOVULNCHECK_VERSION ./...' to see why."
    echo "   ✅ govulncheck"

    go test ./... >/dev/null || die "Go tests failed. Run 'go test ./...' to see why."
    echo "   ✅ go test"
fi

# --- 2. Calculate the new version -----------------------------------------

# The last tag reachable from here, not the highest one in the repository: a
# tag on another branch is not what this branch released.
CURRENT_TAG=$(git describe --tags --abbrev=0 --match 'v*' 2>/dev/null) || \
    die "No v* tag found to compute the next version from."
CURRENT_FULL=${CURRENT_TAG#v}
echo "ℹ️  Current Version: $CURRENT_FULL"

BASE_VERSION=$(echo "$CURRENT_FULL" | sed -E 's/-(Beta|RC)\.[0-9]+$//')

case $TYPE in
    beta)
        if [[ "$CURRENT_FULL" == *"-Beta."* ]]; then
            # Increment existing Beta
            LAST_NUM=$(echo "$CURRENT_FULL" | grep -oP 'Beta\.\K[0-9]+')
            NEXT_NUM=$((10#$LAST_NUM + 1))
        elif [[ "$CURRENT_FULL" == *"-RC."* ]]; then
            # Switching from RC back to Beta (unusual but possible)
            NEXT_NUM=1
        else
            # From stable, bump the patch first: 1.2.0 -> 1.2.1-Beta.01.
            # Reusing the same base would produce a version that sorts *below*
            # the stable it came from.
            IFS='.' read -r -a PARTS <<< "$BASE_VERSION"
            MAJOR="${PARTS[0]}"
            MINOR="${PARTS[1]}"
            PATCH="${PARTS[2]}"
            NEW_PATCH=$((PATCH + 1))
            BASE_VERSION="$MAJOR.$MINOR.$NEW_PATCH"
            NEXT_NUM=1
            echo "ℹ️  Bumping patch for new Beta cycle: $BASE_VERSION"
        fi

        PADDED=$(printf "%02d" $NEXT_NUM)
        NEW_VERSION="$BASE_VERSION-Beta.$PADDED"
        NEW_TAG="v$NEW_VERSION"
        ;;

    rc)
        if [[ "$CURRENT_FULL" == *"-RC."* ]]; then
            LAST_NUM=$(echo "$CURRENT_FULL" | grep -oP 'RC\.\K[0-9]+')
            NEXT_NUM=$((10#$LAST_NUM + 1))
        else
            # From Beta or Stable to RC: keep the same base version
            NEXT_NUM=1
        fi

        PADDED=$(printf "%02d" $NEXT_NUM)
        NEW_VERSION="$BASE_VERSION-RC.$PADDED"
        NEW_TAG="v$NEW_VERSION"
        ;;

    stable)
        # 1.1.8-Beta.07 -> 1.1.8
        NEW_VERSION="$BASE_VERSION"
        NEW_TAG="v$NEW_VERSION"

        echo "Current Version: $CURRENT_FULL"
        echo "Proposed Stable: $NEW_VERSION"
        ;;
esac

# An explicit X.Y.Z wins over the computed one. Stable-from-stable needs it: the
# computed version is the one already tagged.
if [ -n "$FORCED_VERSION" ]; then
    NEW_VERSION="$FORCED_VERSION"
    NEW_TAG="v$NEW_VERSION"
    echo "ℹ️  Version given on the command line: $NEW_VERSION"
fi

git rev-parse --verify --quiet "refs/tags/$NEW_TAG" >/dev/null && \
    die "Tag $NEW_TAG already exists. Pass the target version, e.g. '$0 $TYPE 1.2.3'."

echo "🚀 Preparing Release: $NEW_TAG ($TYPE)"

if [ "$TYPE" == "stable" ]; then
    # Spelled out rather than "this will push to production": a beta pushes too.
    # What only a stable does is move Docker's `latest`.
    echo "   This tags $NEW_TAG and moves the Docker latest tag, which every"
    echo "   agent installed from the documented compose file pulls."
    if [ "$DRY_RUN" = "1" ]; then
        echo "   [dry-run] would ask for confirmation here (skip it with --yes)"
    elif [ "$ASSUME_YES" = "1" ]; then
        echo "   Confirmed with --yes."
    else
        read -r -p "   Continue? (y/N) " confirm || confirm=""
        if [[ $confirm != [yY] && $confirm != [yY][eE][sS] ]]; then
            echo "❌ Cancelled"
            exit 1
        fi
    fi
fi

# --- 3. Release title ------------------------------------------------------

# The one-line summary of the release. It heads the notes; it is NOT a changelog
# entry. Committed as a feature, every beta title would come back as a feature
# of the next stable, usually duplicating the commit it summarised.
if [ -z "$TITLE" ] && [ "$DRY_RUN" = "0" ]; then
    echo "Title for this release (one line, shown at the top of the notes):"
    read -r TITLE || TITLE=""
fi
[ -n "$TITLE" ] || TITLE="minor updates"
echo "🏷️  Title: $TITLE"

# --- 5. Generate the changelog ---------------------------------------------

if [ "$TYPE" == "stable" ]; then
    LAST_TAG=$(git tag -l "v*" --sort=-v:refname | grep -v -E 'Beta|RC' | head -n 1 || true)
    if [ -z "$LAST_TAG" ]; then
        LAST_TAG=$(git describe --tags --abbrev=0 2>/dev/null || true)
    fi
    echo "📝 Generating stable changelog (aggregating since $LAST_TAG)..."
else
    LAST_TAG=$(git describe --tags --abbrev=0 2>/dev/null || true)
    echo "📝 Generating changelog from $LAST_TAG..."
fi
[ -n "$LAST_TAG" ] || die "No previous tag found to build a changelog from."

# Commits that describe the release process rather than the release. `release:`
# is the title of every version tagged in this range: for a stable that spans a
# beta cycle, every one of those betas contributed its own, and they are the
# headline of their own notes, not a change.
#
# `test:`, `ci:`, `style:` and `docs:` are dropped too: the notes are read by
# someone deciding whether to update, and none of them changes anything they run.
# A formatting pass that reaches the public notes as "Gofmt the tests" tells
# that reader nothing at all.
#
# `docs:` is the same, only more so. The README is published the moment it is
# pushed, so those changes reach readers whether or not anyone takes the
# release, and a run of documentation lines pushes the one behavioural change
# out of sight. Where documentation is worth announcing, the notes are written
# by hand (a hand-written file always wins, see below).
#
# The `(docs)` scope is dropped on any type, not just `docs:`: a `fix(docs)` is
# a repair to something that produces the documentation, nothing a reader of
# these notes will ever run. `fix(docker)` is untouched: the scope has to be
# exactly `docs`.
#
# Which puts weight on typing a commit correctly. `docs:` means the published
# documentation and nothing else. A change to anything the binary or the image
# ships is a `fix:` or a `feat:`, however documentary it feels.
HOUSEKEEPING='^release:|^chore\(release\)|^docs\(release\)|^chore: cleanup|^chore: bump|pre-release|^Merge branch|^Merge remote-tracking|^test(\([a-z0-9_ -]+\))?!?:|^ci(\([a-z0-9_ -]+\))?!?:|^style(\([a-z0-9_ -]+\))?!?:|^docs(\([a-z0-9_ -]+\))?!?:|^[a-z]+\(docs\)!?:|^[a-z]+\(ci\)!?:'

# One normalizer for every section. The previous code had two — a decent regex
# for the typed buckets and `sed 's/^[a-z]*: //'` for everything else, which only
# matched a bare `word:`. Anything with a scope survived and got capitalized on
# its way into the public notes: "Ci(release): …", "Docs(update): …", "I18n: …".
CONVENTIONAL_TYPES='feat|fix|refactor|perf|chore|docs|style|build|ci|test|i18n|revert|remove'

clean_subjects() {
    grep -vE "$HOUSEKEEPING" | \
    grep -viE '^(feat|fix|chore)?(\([a-z0-9_-]+\))?:? *minor updates$' | \
    sed -E "s/^($CONVENTIONAL_TYPES)(\([a-z0-9_ -]+\))?(!)?: *//I" | \
    awk 'NF {$1=toupper(substr($1,1,1))substr($1,2); print "- " $0}' | \
    sort -u
}

subjects() { git log "$LAST_TAG..HEAD" --pretty=format:"%s"; }

FEATS=$(subjects | grep -iE "^feat" | clean_subjects || echo "")
FIXES=$(subjects | grep -iE "^fix" | clean_subjects || echo "")
REFACTORS=$(subjects | grep -iE "^(refactor|perf)" | clean_subjects || echo "")
# Keep-a-Changelog has a Removed section and this had no bucket for it, so a
# withdrawal had to be committed as feat and came out under "Added", reading to
# anyone scanning the notes as the opposite of what happened. Commit a
# withdrawal as `remove:`.
REMOVALS=$(subjects | grep -iE "^remove" | clean_subjects || echo "")
OTHER=$(subjects | grep -viE "^(feat|fix|refactor|perf|remove)" | clean_subjects || echo "")

# A hand-written file always wins: the generated one is a list of commit
# subjects, which is not what a reader wants. It wins for the changelog of a
# stable too — otherwise a subject the notes were written to leave out would
# still land in CHANGELOG.md.
NOTES_FILE="RELEASE_NOTES_$NEW_TAG.md"
HAND_WRITTEN=""
[ -f "$NOTES_FILE" ] && HAND_WRITTEN=$(cat "$NOTES_FILE")

# --- What is published names no other product ------------------------------
#
# Checked before the changelog, the notes, the tag or the push, and in a dry
# run as well: the title, and the notes as written or as generated, which on a
# stable are also the changelog entry. The notes come from commit subjects, so
# one subject saying where something came from is a line on the release page.
# A subject cannot be changed once pushed; the way out is to write the notes by
# hand.
if [ -n "$HAND_WRITTEN" ]; then
    PUBLISHED=$(printf '%s\n' "$TITLE" "$HAND_WRITTEN")
else
    PUBLISHED=$(printf '%s\n' "$TITLE" "$FEATS" "$FIXES" "$REFACTORS" "$REMOVALS" "$OTHER")
fi
NAMED=$(IFS='|'; printf '%s\n' "$PUBLISHED" | grep -inwE "${OTHER_PRODUCTS[*]}" || true)
if [ -n "$NAMED" ]; then
    # Nothing has been written yet, so the run can simply be repeated.
    die "what this release would publish names another product of the house:

$(printf '%s\n' "$NAMED" | sed 's/^[0-9]*:/    /')

    What a project took from another is its own now, and the notes say what it
    does. Write $NOTES_FILE by hand without it and run this again — a
    hand-written file always wins, for the notes and for the changelog."
fi

# Update CHANGELOG.md
if [ "$TYPE" == "stable" ] && [ -f "CHANGELOG.md" ]; then
    echo "📝 Updating CHANGELOG.md..."
    DATE=$(date +%Y-%m-%d)
    VERSION_DISPLAY=$(echo "$NEW_TAG" | sed 's/^v//')

    CL_CHANGED=""
    [ -n "$REFACTORS" ] && CL_CHANGED="$REFACTORS"
    [ -n "$OTHER" ] && CL_CHANGED="${CL_CHANGED:+$CL_CHANGED$'\n'}$OTHER"

    # Build the entry in a temp file with REAL newlines. (Composing it in a
    # shell var via $(echo -e '…\n') strips trailing newlines and runs the
    # section headers together, e.g. "…home### Fixed".)
    ENTRY_FILE=$(mktemp)
    {
        printf '## [%s] - %s\n\n' "$VERSION_DISPLAY" "$DATE"
        if [ -n "$HAND_WRITTEN" ]; then
            # The hand-written notes, without the bold title line that heads
            # the release page.
            printf '%s\n' "$HAND_WRITTEN" | sed '1{/^\*\*.*\*\*$/d}' | sed '/./,$!d'
            printf '\n'
        else
            [ -n "$FEATS" ]      && printf '### Added\n%s\n\n' "$FEATS"
            [ -n "$FIXES" ]      && printf '### Fixed\n%s\n\n' "$FIXES"
            [ -n "$CL_CHANGED" ] && printf '### Changed\n%s\n\n' "$CL_CHANGED"
            [ -n "$REMOVALS" ]   && printf '### Removed\n%s\n\n' "$REMOVALS"
        fi
        true
    } > "$ENTRY_FILE"

    if [ "$DRY_RUN" = "1" ]; then
        echo "   [dry-run] CHANGELOG.md entry that would be inserted:"
        sed 's/^/     /' "$ENTRY_FILE"
    else
        # Insert before the first version header
        awk 'NR==FNR { entry = entry $0 "\n"; next }
             /^## \[/ && !done { printf "%s", entry; done=1 }
             { print }' "$ENTRY_FILE" CHANGELOG.md > CHANGELOG.md.tmp \
            && mv CHANGELOG.md.tmp CHANGELOG.md
    fi
    rm -f "$ENTRY_FILE"
fi

# Release body for Forgejo. A hand-written file always wins (see above).
if [ -f "$NOTES_FILE" ]; then
    echo "📄 Using existing release notes: $NOTES_FILE"
else
    NOTES_CHANGED=""
    if [ -n "$REFACTORS" ] && [ -n "$OTHER" ]; then
        NOTES_CHANGED=$(printf '%s\n%s' "$REFACTORS" "$OTHER")
    elif [ -n "$REFACTORS" ]; then
        NOTES_CHANGED="$REFACTORS"
    else
        NOTES_CHANGED="$OTHER"
    fi

    # The title leads the notes. It is the one line a reader sees first, and
    # keeping it out of the sections is what stops it from also being counted as
    # a feature of the next stable.
    compose_notes() {
        printf '**%s**\n\n' "$TITLE"
        [ -n "$FEATS" ]         && printf '### Added\n%s\n\n' "$FEATS"
        [ -n "$FIXES" ]         && printf '### Fixed\n%s\n\n' "$FIXES"
        [ -n "$NOTES_CHANGED" ] && printf '### Changed\n%s\n' "$NOTES_CHANGED"
        [ -n "$REMOVALS" ]      && printf '### Removed\n%s\n' "$REMOVALS"
        true
    }

    if [ "$DRY_RUN" = "1" ]; then
        echo "   [dry-run] $NOTES_FILE would be generated as:"
        compose_notes | sed 's/^/     /'
    else
        compose_notes > "$NOTES_FILE"
    fi
fi

# --- 6. Commit, tag, push --------------------------------------------------

if [ "$DRY_RUN" = "1" ]; then
    echo "📦 Would commit the notes, tag $NEW_TAG and push:"
    echo "   [dry-run] git add CHANGELOG.md $NOTES_FILE"
    echo "   [dry-run] git commit -m 'chore(release): bump to $NEW_TAG'"
    echo "   [dry-run] git tag $NEW_TAG"
    echo "   [dry-run] git push origin $RELEASE_BRANCH && git push origin $NEW_TAG"
    echo "   [dry-run] rm $NOTES_FILE, commit cleanup, push"
    echo
    echo "🧪 Dry run complete — the working tree is untouched."
    exit 0
fi

echo "📦 Committing release notes..."
[ -f "CHANGELOG.md" ] && git add CHANGELOG.md
[ -f "$NOTES_FILE" ] && git add "$NOTES_FILE"
# The notes may already be committed, in which case there is nothing staged.
if ! git diff --cached --quiet; then
    git commit -m "chore(release): bump to $NEW_TAG"
fi

git tag "$NEW_TAG"

echo "📤 Pushing to Remote..."
# The tag goes last on purpose: it is what the CI builds from, so it must never
# point at commits the branch has not accepted.
git push origin "$RELEASE_BRANCH" || die "Push to $RELEASE_BRANCH failed. The tag $NEW_TAG stays local; delete it with 'git tag -d $NEW_TAG' once resolved."
git push origin "$NEW_TAG" || die "Tag push failed. $RELEASE_BRANCH is already pushed; retry with 'git push origin $NEW_TAG'."

# Cleanup: the notes live on in the tag, but are removed from the branch so the
# root does not collect one file per release. Only this release's file.
if [ -f "$NOTES_FILE" ]; then
    rm -f "$NOTES_FILE"
    git add "$NOTES_FILE"
    git commit -m "chore(release): cleanup release notes [skip ci]"
    git push origin "$RELEASE_BRANCH"
fi

echo "✅ Successfully deployed $NEW_TAG!"
