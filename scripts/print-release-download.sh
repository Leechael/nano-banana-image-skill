#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage:
  scripts/print-release-download.sh <tag> [repo-dir]

Examples:
  scripts/print-release-download.sh v1.2.3
  scripts/print-release-download.sh v1.2.3 /path/to/repo
USAGE
}

tag="${1:-}"
repo_dir="${2:-.}"

if [[ -z "$tag" ]]; then
  usage
  exit 1
fi

env_file="$repo_dir/release-naming.env"
if [[ ! -f "$env_file" ]]; then
  echo "missing naming contract file: $env_file" >&2
  exit 1
fi

# shellcheck disable=SC1090
source "$env_file"

if [[ -z "${ARTIFACT_GLOB:-}" ]]; then
  echo "ARTIFACT_GLOB is empty in $env_file" >&2
  exit 1
fi

cat <<EOF
# Download release assets matching naming contract
gh release download "$tag" --pattern "$ARTIFACT_GLOB"
EOF
