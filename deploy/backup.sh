#!/usr/bin/env bash
# M5 每日备份：pg_dump（custom format）→ Cloudflare R2（S3 兼容 API）。
#
# 依赖（二选一）：
#   A) aws cli v2：AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY 指向 R2 token
#   B) rclone：R2 remote 已配置（RCLONE_R2_REMOTE=r2）
#
# 必填环境变量：
#   DATABASE_URL        Postgres 连接串（Supabase pooler 6543 或直连 5432）
#   R2_BUCKET           R2 桶名
#   R2_ENDPOINT         https://<accountid>.r2.cloudflarestorage.com
#   R2_ACCESS_KEY_ID / R2_SECRET_ACCESS_KEY
# 可选：
#   BACKUP_PREFIX       default: newapi
#   BACKUP_RETAIN_DAYS  default: 14（仅打印保留策略；R2 生命周期规则建议在控制台配置）
#   PGPASSWORD          postgres 密码（或直接写在 DATABASE_URL）
#
# 用法：
#   DATABASE_URL=... R2_BUCKET=... ./deploy/backup.sh
#   Railway Cron / 系统 cron：0 3 * * * cd /app && ./deploy/backup.sh
set -euo pipefail

: "${DATABASE_URL:?DATABASE_URL is required}"
: "${R2_BUCKET:?R2_BUCKET is required}"
: "${R2_ENDPOINT:?R2_ENDPOINT is required}"
: "${R2_ACCESS_KEY_ID:?R2_ACCESS_KEY_ID is required}"
: "${R2_SECRET_ACCESS_KEY:?R2_SECRET_ACCESS_KEY is required}"

BACKUP_PREFIX="${BACKUP_PREFIX:-newapi}"
BACKUP_RETAIN_DAYS="${BACKUP_RETAIN_DAYS:-14}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
OBJECT_KEY="${BACKUP_PREFIX}/db-${STAMP}.dump"
TMP_FILE="$(mktemp "${TMPDIR:-/tmp}/newapi-backup-${STAMP}.XXXXXX.dump")"
trap 'rm -f "${TMP_FILE}"' EXIT

echo "[backup] pg_dump → ${TMP_FILE}"
# --no-owner/--no-acl：Supabase 角色与目标恢复库可能不同，保留数据不保留授权。
pg_dump "${DATABASE_URL}" \
  --format=custom \
  --no-owner \
  --no-acl \
  --file="${TMP_FILE}"

SIZE="$(wc -c < "${TMP_FILE}" | tr -d ' ')"
if [ "${SIZE}" -lt 1024 ]; then
  echo "[backup] ERROR: dump is suspiciously small (${SIZE} bytes)" >&2
  exit 1
fi
echo "[backup] dump size: ${SIZE} bytes"

if command -v aws >/dev/null 2>&1; then
  echo "[backup] upload via aws cli → s3://${R2_BUCKET}/${OBJECT_KEY}"
  AWS_ACCESS_KEY_ID="${R2_ACCESS_KEY_ID}" \
  AWS_SECRET_ACCESS_KEY="${R2_SECRET_ACCESS_KEY}" \
  AWS_DEFAULT_REGION=auto \
    aws --endpoint-url "${R2_ENDPOINT}" s3 cp "${TMP_FILE}" "s3://${R2_BUCKET}/${OBJECT_KEY}"
elif command -v rclone >/dev/null 2>&1; then
  REMOTE="${RCLONE_R2_REMOTE:-r2}"
  echo "[backup] upload via rclone → ${REMOTE}:${R2_BUCKET}/${OBJECT_KEY}"
  rclone copyto "${TMP_FILE}" "${REMOTE}:${R2_BUCKET}/${OBJECT_KEY}" \
    --s3-endpoint "${R2_ENDPOINT}"
else
  echo "[backup] ERROR: neither aws cli nor rclone is installed" >&2
  exit 1
fi

echo "[backup] OK: ${OBJECT_KEY}"
echo "[backup] retention: ${BACKUP_RETAIN_DAYS} days (configure an R2 lifecycle rule on prefix ${BACKUP_PREFIX}/)"
echo "[backup] restore example: pg_restore --clean --if-exists --no-owner --no-acl -d \"\$TARGET_DSN\" db-${STAMP}.dump"
