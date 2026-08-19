#!/usr/bin/env bash
# migrations/*.sql を番号順にすべて適用する。全ファイル冪等 (IF NOT EXISTS) 規約なので
# 何度実行しても安全。DB が無い新環境でも CREATE DATABASE から行う。
#
# 実行場所はどちらでもよい:
#   踏み台の上で:  DB_PASSWORD=... scripts/migrate.sh
#   手元 / CD から: BASTION_HOST=<踏み台のグローバルIP> DB_PASSWORD=... scripts/migrate.sh
#     (BASTION_HOST が設定されていれば ssh ubuntu@$BASTION_HOST 経由で mysql を叩く。
#      IP は infra/terraform-apprun で terraform output bastion_ip でも確認できる)
#
# 環境変数 (env.sh の TF_VAR_db_password があればパスワードはそれを使う):
#   DB_HOST (default 192.168.1.30) / DB_USER (default sakuravel_app)
#   DB_NAME (default sakuravel_app) / DB_PASSWORD / BASTION_HOST / BASTION_SSH_KEY_PATH
set -euo pipefail

cd "$(dirname "$0")/.."

DB_HOST="${DB_HOST:-192.168.1.30}"
DB_USER="${DB_USER:-sakuravel_app}"
DB_NAME="${DB_NAME:-sakuravel_app}"
DB_PASSWORD="${DB_PASSWORD:-${TF_VAR_db_password:?DB_PASSWORD か TF_VAR_db_password を設定してください}}"
BASTION_HOST="${BASTION_HOST:-}"
BASTION_SSH_KEY_PATH="${BASTION_SSH_KEY_PATH:-}"

# mysql の実行ラッパー。BASTION_HOST があれば踏み台経由、無ければその場で実行する。
# SQL は標準入力から渡す (SSH でもクォート事故が起きないように)。
run_mysql() {
  local db="$1"
  if [ -n "$BASTION_HOST" ]; then
    local ssh_opts=(-o ConnectTimeout=10)
    [ -n "$BASTION_SSH_KEY_PATH" ] && ssh_opts+=(-i "$BASTION_SSH_KEY_PATH" -o IdentitiesOnly=yes)
    ssh "${ssh_opts[@]}" ubuntu@"$BASTION_HOST" \
      "mysql -h $DB_HOST -u $DB_USER -p'$DB_PASSWORD' $db"
  else
    mysql -h "$DB_HOST" -u "$DB_USER" -p"$DB_PASSWORD" $db
  fi
}

# DB 自体の作成は Terraform 管理外 (sakura_database に DB 名の属性が無い) ため、ここで保証する
echo "==== ensure database: ${DB_NAME} ===="
echo "CREATE DATABASE IF NOT EXISTS ${DB_NAME} CHARACTER SET utf8mb4;" | run_mysql ""

shopt -s nullglob
files=(migrations/*.sql)
if [ ${#files[@]} -eq 0 ]; then
  echo "migrations/*.sql が見つかりません" >&2
  exit 1
fi

for f in "${files[@]}"; do
  echo "==== apply: ${f} ===="
  run_mysql "$DB_NAME" < "$f"
done

echo "all migrations applied."
