#!/bin/sh
# ベンチ前初期化。POST /initialize から呼ばれる。
#
# 生成データ(initial-data/out)の読み込みは ISUBID_INITIAL_DATA_DIR による明示的オプトイン。
# 未設定ならスキーマとシードだけを流す。生成物はリポジトリにコミットされるため
# 「ファイルが存在すれば読む」では条件として機能しない(webapp/go のテストが
# 生成データを読み込んでしまい、値ベースのアサーションが全滅する)。
set -eu

: "${ISUBID_DB_HOST:=127.0.0.1}"
: "${ISUBID_DB_PORT:=3306}"
: "${ISUBID_DB_USER:=isucon}"
: "${ISUBID_DB_PASSWORD:=isucon}"
: "${ISUBID_DB_NAME:=isubid}"

SQL_DIR="$(cd "$(dirname "$0")" && pwd)"

run() {
  mysql -h "$ISUBID_DB_HOST" -P "$ISUBID_DB_PORT" \
        -u "$ISUBID_DB_USER" -p"$ISUBID_DB_PASSWORD" \
        --default-character-set=utf8mb4 \
        "$ISUBID_DB_NAME" < "$1"
}

run "$SQL_DIR/00_schema.sql"
run "$SQL_DIR/90_seed_phase1.sql"

if [ -n "${ISUBID_INITIAL_DATA_DIR:-}" ]; then
  for f in 91_users.sql 92_auctions.sql 93_bids.sql 94_notifications.sql; do
    run "$ISUBID_INITIAL_DATA_DIR/$f"
  done
fi
