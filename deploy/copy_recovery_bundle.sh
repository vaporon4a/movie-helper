#!/usr/bin/env bash

set -euo pipefail

umask 077

script_dir=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)

ssh_target="${MOVIE_HELPER_SSH_TARGET:-}"
identity_file="$HOME/.ssh/movie-helper_server"
output_dir="$HOME/movie-helper-recovery-$(date -u +%Y%m%dT%H%M%SZ)"
plan_file="$repo_root/claude/tasks/SERVER_OS_REINSTALL_RECOVERY.md"

usage() {
  cat <<'EOF'
Usage: deploy/copy_recovery_bundle.sh [options]

Copy the files required to restore Movie Helper after reinstalling the VPS.
Secret values are copied into a private local directory and are not printed.

Options:
  --host USER@HOST    SSH target (required unless MOVIE_HELPER_SSH_TARGET is set)
  --identity PATH     SSH private key (default: ~/.ssh/movie-helper_server)
  --output PATH       New local recovery directory
  -h, --help          Show this help
EOF
}

while (($# > 0)); do
  case "$1" in
    --host)
      (($# >= 2)) || { printf 'Missing value for --host\n' >&2; exit 2; }
      ssh_target=$2
      shift 2
      ;;
    --identity)
      (($# >= 2)) || { printf 'Missing value for --identity\n' >&2; exit 2; }
      identity_file=$2
      shift 2
      ;;
    --output)
      (($# >= 2)) || { printf 'Missing value for --output\n' >&2; exit 2; }
      output_dir=$2
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      printf 'Unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

case "$identity_file" in
  \~/*) identity_file="$HOME/${identity_file#\~/}" ;;
esac
case "$output_dir" in
  \~/*) output_dir="$HOME/${output_dir#\~/}" ;;
esac

for command_name in ssh scp python3 sha256sum; do
  command -v "$command_name" >/dev/null 2>&1 || {
    printf 'Required command is not installed: %s\n' "$command_name" >&2
    exit 1
  }
done

[[ -n $ssh_target && $ssh_target != -* && $ssh_target != *[[:space:]]* ]] || {
  printf 'Set a valid SSH target with --host USER@HOST\n' >&2
  exit 2
}
[[ -f $identity_file ]] || {
  printf 'SSH identity not found: %s\n' "$identity_file" >&2
  exit 1
}
[[ ! -e $output_dir ]] || {
  printf 'Output path already exists: %s\n' "$output_dir" >&2
  exit 1
}

output_parent=$(dirname -- "$output_dir")
mkdir -p -- "$output_parent"
staging_dir=$(mktemp -d "$output_dir.tmp.XXXXXX")
chmod 700 "$staging_dir"
cleanup() {
  rm -rf -- "$staging_dir"
}
trap cleanup EXIT

ssh_options=(
  -i "$identity_file"
  -o IdentitiesOnly=yes
  -o BatchMode=yes
  -o StrictHostKeyChecking=yes
)

printf 'Checking recovery files on %s...\n' "$ssh_target"
ssh "${ssh_options[@]}" "$ssh_target" '
  set -eu
  test -r /opt/movie-helper/secrets/rclone.conf
  test -r /opt/movie-helper/backup-last-success.json
  test "$(stat -c %a /opt/movie-helper/secrets/rclone.conf)" = 600
  grep -q "^\[b2\]$" /opt/movie-helper/secrets/rclone.conf
  grep -q "^\[backup\]$" /opt/movie-helper/secrets/rclone.conf
'

printf 'Copying encrypted-backup configuration and success marker...\n'
scp "${ssh_options[@]}" -- \
  "$ssh_target:/opt/movie-helper/secrets/rclone.conf" \
  "$staging_dir/rclone.conf"
scp "${ssh_options[@]}" -- \
  "$ssh_target:/opt/movie-helper/backup-last-success.json" \
  "$staging_dir/backup-last-success.json"

chmod 600 \
  "$staging_dir/rclone.conf" \
  "$staging_dir/backup-last-success.json"
if [[ -f $plan_file ]]; then
  cp -- "$plan_file" "$staging_dir/SERVER_OS_REINSTALL_RECOVERY.md"
  chmod 600 "$staging_dir/SERVER_OS_REINSTALL_RECOVERY.md"
fi

archive_name=$(python3 - "$staging_dir/backup-last-success.json" <<'PY'
import json
import re
import sys

with open(sys.argv[1], encoding="utf-8") as marker_file:
    marker = json.load(marker_file)

archive = marker.get("archive")
verified_at = marker.get("verified_at")
size_bytes = marker.get("size_bytes")

if not isinstance(archive, str) or not re.fullmatch(
    r"daily-\d{8}T\d{6}Z-[0-9a-f]+\.tar\.gz", archive
):
    raise SystemExit("backup marker contains an invalid archive name")
if not isinstance(verified_at, str) or not verified_at:
    raise SystemExit("backup marker has no verification time")
if not isinstance(size_bytes, int) or size_bytes <= 0:
    raise SystemExit("backup marker contains an invalid archive size")

print(archive)
PY
)

ssh "${ssh_options[@]}" "$ssh_target" '
  set -eu
  printf "captured_at_utc=%s\n" "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf "hostname=%s\n" "$(hostname)"
  printf "current_release=%s\n" "$(readlink -f /opt/movie-helper/current)"
  docker inspect movie-helper-bot-1 \
    --format "container_status={{.State.Status}}\nrestart_count={{.RestartCount}}\nimage={{.Config.Image}}"
' >"$staging_dir/server-state.txt"
chmod 600 "$staging_dir/server-state.txt"

(
  cd "$staging_dir"
  checksum_files=(rclone.conf backup-last-success.json server-state.txt)
  if [[ -f SERVER_OS_REINSTALL_RECOVERY.md ]]; then
    checksum_files+=(SERVER_OS_REINSTALL_RECOVERY.md)
  fi
  sha256sum "${checksum_files[@]}" >SHA256SUMS
  chmod 600 SHA256SUMS
)

mv -- "$staging_dir" "$output_dir"
trap - EXIT

printf 'Recovery bundle created: %s\n' "$output_dir"
printf 'Referenced B2 archive: %s\n' "$archive_name"
printf 'Directory mode: 700; file modes: 600. Keep this directory private.\n'
