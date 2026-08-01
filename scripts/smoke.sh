#!/bin/sh
set -eu

api_url=${API_SMOKE_URL:-http://127.0.0.1:8080}
web_url=${WEB_SMOKE_URL:-http://127.0.0.1:3000}

fetch() {
  curl --fail --silent --show-error --max-time 15 "$1"
}

health=$(fetch "$api_url/healthz")
printf '%s' "$health" | grep -q '"status":"ok"'

ready=$(fetch "$api_url/readyz")
printf '%s' "$ready" | grep -q '"status":"ready"'

trending=$(fetch "$api_url/v1/trending?window=24h&limit=1")
printf '%s' "$trending" | grep -q '"demoMode":true'
printf '%s' "$trending" | grep -q '"formulaVersion":"momentum-v1"'

badge_headers=$(curl --fail --silent --show-error --head --max-time 15 \
  "$api_url/v1/badges/orbit-labs/signal-kit.svg?window=24h")
printf '%s' "$badge_headers" | grep -qi '^content-type: image/svg+xml'

feed_headers=$(curl --fail --silent --show-error --head --max-time 15 \
  "$api_url/v1/feeds/trending.xml?window=24h&limit=1")
printf '%s' "$feed_headers" | grep -qi '^content-type: application/atom+xml'

home=$(fetch "$web_url")
printf '%s' "$home" | grep -q 'Catch rising repositories'

printf 'RepoMeteor smoke checks passed: %s and %s\n' "$api_url" "$web_url"
