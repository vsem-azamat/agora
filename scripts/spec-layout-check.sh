#!/usr/bin/env bash
# Enforces the openspec/specs/ layout: a README.md index per capability, sub-capability
# spec.md files exactly one level below, every sub-spec linked from its capability index,
# every capability linked from the root index.
set -euo pipefail
cd "$(dirname "$0")/.."
root=openspec/specs
fail=0
err() { echo "spec-layout: $*" >&2; fail=1; }

[ -f "$root/README.md" ] || err "missing $root/README.md"

for cap_dir in "$root"/*/; do
  [ -d "$cap_dir" ] || continue
  cap="$(basename "$cap_dir")"
  [ -f "$cap_dir/README.md" ] || err "missing index $cap_dir/README.md"
  [ ! -f "$cap_dir/spec.md" ] || err "flat spec $cap_dir/spec.md; split it into sub-capability directories"
  grep -q "($cap/README.md)" "$root/README.md" || err "$cap is not linked from $root/README.md"
  found=0
  for sub_dir in "$cap_dir"*/; do
    [ -d "$sub_dir" ] || continue
    sub="$(basename "$sub_dir")"
    found=1
    [ -f "$sub_dir/spec.md" ] || err "missing $sub_dir/spec.md"
    grep -q "($sub/spec.md)" "$cap_dir/README.md" || err "$cap/$sub is not linked from $cap_dir/README.md"
    deeper="$(find "$sub_dir" -mindepth 2 -name spec.md | head -1 || true)"
    [ -z "$deeper" ] || err "spec nested too deep: $deeper"
  done
  [ "$found" = 1 ] || err "$cap has no sub-capability directories"
done

[ "$fail" = 0 ] && echo "spec-layout: ok"
exit "$fail"
