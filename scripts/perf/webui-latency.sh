#!/usr/bin/env bash
# WebUI latency / payload harness for a running `lele gateway`.
#
# Read-only by design: it only issues GETs/HEADs against the target and never
# mutates anything on the host. Usage:
#   ./scripts/perf/webui-latency.sh [base_url] [seconds]
# Defaults: http://127.0.0.1:18790  30
#
# The default target is localhost on purpose: the sampling loops run for
# ~3x SECONDS_SAMPLE and would otherwise point an unattended load generator at
# whatever host the last author was debugging. Pass a URL explicitly to probe a
# remote gateway.
#
# Every request is capped by --max-time so a stalled gateway (the very
# signature this harness measures) cannot hang the harness indefinitely;
# timed-out samples are counted and reported instead of blocking the loop.
#
# Reports:
#   1. latency percentiles for /health and one auth-protected endpoint
#   2. per-minute request rate of the /subagents endpoint (the storm probe)
#   3. cold-load payload of index.html + every referenced asset,
#      with and without Accept-Encoding: gzip
set -uo pipefail

BASE="${1:-http://127.0.0.1:18790}"
SECONDS_SAMPLE="${2:-30}"
BASE="${BASE%/}"

# A stalled gateway is exactly what this harness measures, so no request may
# block forever: every call is capped and counted as a timeout.
MAX_TIME="${LELE_PERF_MAX_TIME:-5}"
c() { curl -s --max-time "$MAX_TIME" "$@"; }

pct() { # percentile from stdin numbers
  sort -n | awk -v p="${1:-0.99}" '{a[NR]=$1} END{if(NR==0){print "n/a";exit} print a[int(NR*p+0.999999)]}'
}

sample() { # url label
  local url="$1" label="$2"
  local out
  out=$(mktemp)
  local end=$((SECONDS + SECONDS_SAMPLE))
  local timeouts=0
  while [ $SECONDS -lt $end ]; do
    if ! c -o /dev/null -w "%{time_total}\n" "$url" >>"$out"; then
      timeouts=$((timeouts + 1))
    fi
  done
  local n max p50 p95 p99
  n=$(wc -l <"$out")
  p50=$(pct 0.50 <"$out"); p95=$(pct 0.95 <"$out"); p99=$(pct 0.99 <"$out")
  max=$(sort -n "$out" | tail -1)
  printf '%-22s n=%-6s p50=%ss p95=%ss p99=%ss max=%ss\n' "$label" "$n" "$p50" "$p95" "$p99" "$max"
  [ "$timeouts" -gt 0 ] && printf '  !! %s: %d request(s) hit the %ss cap (or failed)\n' "$label" "$timeouts" "$MAX_TIME"
  awk -v t="$max" 'BEGIN{exit !(t+0>0.5)}' && echo "  !! $label: a sample exceeded 500ms (stall signature)"
  rm -f "$out"
}

echo "== target: $BASE (sampling ${SECONDS_SAMPLE}s per endpoint) =="
sample "$BASE/health" "/health"
sample "$BASE/api/v1/agents" "/api/v1/agents (401 path)"
sample "$BASE/" "GET / (index.html)"

echo
echo "== subagents poll cost (single sequential request, x10) =="
for i in $(seq 10); do
  c -o /dev/null -w "%{time_total} " "$BASE/api/v1/chat/sessions/probe/subagents"
done
echo "(401s are expected without a token; the point is the latency shape)"

echo
echo "== cold load payload (index.html + referenced assets) =="
idx=$(c "$BASE/")
total_raw=0
total_gz=0
printf '%-44s %10s %10s %s\n' "resource" "bytes" "gzip" "cache"
# Farm emits both quoted (src="/a.js") and unquoted (src=/a.js data-x=true)
# attribute values, so match both shapes or the payload total comes out wrong.
for path in / $(printf '%s\n' "$idx" | grep -oE '(src|href)=(")?/[^" >]+' | sed -E 's/^(src|href)=("?)//'); do
  raw=$(c -o /dev/null -w "%{size_download}" "$BASE$path")
  gz=$(c -H 'Accept-Encoding: gzip' -o /dev/null -w "%{size_download}" "$BASE$path")
  hdr=$(c -I "$BASE$path" | grep -i '^cache-control' | tr -d '\r' | cut -d' ' -f2- || true)
  [ -z "$hdr" ] && hdr="MISSING"
  printf '%-44s %10s %10s %s\n' "$path" "$raw" "$gz" "$hdr"
  total_raw=$((total_raw + raw))
  total_gz=$((total_gz + gz))
done
printf '%-44s %10s %10s\n' "TOTAL" "$total_raw" "$total_gz"
echo "expected once fixed: gzip total <= ~450KB, hashed assets immutable, index.html no-cache"

echo
echo "== revalidation probe (does a reload cost 0 bytes?) =="
asset=$(printf '%s\n' "$idx" | grep -oE '"/[^"]+\.(js|css)"' | tr -d '"' | head -1)
if [ -n "${asset:-}" ]; then
  etag=$(c -I "$BASE$asset" | grep -i '^etag' | tr -d '\r' | sed 's/^[Ee][Tt]ag: //')
  code=$(c -o /dev/null -w "%{http_code}" -H "If-None-Match: $etag" "$BASE$asset")
  printf 'asset=%s etag=%s conditional-request=%s (want 304)\n' "$asset" "${etag:-MISSING}" "$code"
fi
