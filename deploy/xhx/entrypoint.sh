#!/bin/sh
set -e
mkdir -p /app/data
# ?????????????/??/??????
jq --arg k "${XHX_API_KEY:-}" --arg a "${XHX_ADMIN_KEY:-}" \
   '.listen="0.0.0.0:3066" | .api_key=$k | .accounts.file="/app/data/accounts.json" | (if $a=="" then . else .admin.api_key=$a end)' \
   /app/config.template.json > /app/config.json
printf '%s' "${XHX_ACCOUNTS_JSON:-{\"accounts\":[]\}}" > /app/data/accounts.json
exec /app/xhx2api -config /app/config.json
