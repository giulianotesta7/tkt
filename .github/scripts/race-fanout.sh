#!/usr/bin/env bash
# Run one package's tests under -race in parallel processes.
#
# The package's test binary is compiled once, its top-level tests are split
# round-robin into WORKERS groups, and each group runs as its own process, so a
# package whose tests run serially still uses every core. Every listed test runs
# in exactly one group; the script fails if any group fails, nothing is listed,
# or the number of finished top-level tests differs from the listed count.
#
# Usage: race-fanout.sh <package-dir> [workers]   (e.g. ./internal/adapters/http 4)
set -euo pipefail

pkg_dir=${1:?package directory required}
workers=${2:-4}
out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT

go test -c -race -o "$out/pkg.test" "$pkg_dir"

cd "$pkg_dir"
mapfile -t tests < <("$out/pkg.test" -test.list '.*' | grep -E '^(Test|Example|Fuzz)')
if [ "${#tests[@]}" -eq 0 ]; then
  echo "no tests listed in $pkg_dir" >&2
  exit 1
fi
echo "$pkg_dir: ${#tests[@]} tests across $workers workers"

pids=()
for ((w = 0; w < workers; w++)); do
  group=()
  for ((i = w; i < ${#tests[@]}; i += workers)); do group+=("${tests[i]}"); done
  [ "${#group[@]}" -gt 0 ] || continue
  pattern="^($(IFS='|'; echo "${group[*]}"))\$"
  "$out/pkg.test" -test.run "$pattern" -test.count=1 -test.timeout=35m -test.v > "$out/worker-$w.log" 2>&1 &
  pids+=("$!:$w")
done

status=0
ran=0
for entry in "${pids[@]}"; do
  pid=${entry%%:*}
  w=${entry##*:}
  if wait "$pid"; then
    n=$(grep -cE '^--- (PASS|SKIP): ' "$out/worker-$w.log" || true)
    ran=$((ran + n))
    echo "worker $w: ok ($n tests)"
  else
    status=1
    echo "worker $w: FAILED"
    cat "$out/worker-$w.log"
  fi
done
if [ "$status" -eq 0 ] && [ "$ran" -ne "${#tests[@]}" ]; then
  echo "ran $ran top-level tests, listed ${#tests[@]}: a test was skipped or run twice" >&2
  status=1
fi
exit $status
