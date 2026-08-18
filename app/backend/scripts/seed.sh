#!/usr/bin/env bash
# ダミーデータを投入する。Go を手元に入れていなくてもコンテナ経由で動く。
# 投入先は空の DB であること。seeder は固定のユーザー名 (user00001…) を素の INSERT で
# 入れるが users.username は UNIQUE なので、投入済みの DB に重ねると error 1062 で落ちる。
#
#   scripts/seed.sh --reset 10 # DB を作り直してから scale 10 で投入（通常はこちら）
#   scripts/seed.sh 10         # 空の DB に scale 10 で投入
#
# --reset は docker compose down -v を通るので DB のデータが全部消える。
set -euo pipefail

cd "$(dirname "$0")/.."

GO_IMAGE=golang:1.25-alpine
MODCACHE_VOLUME=gomodcache
NETWORK=app-network

reset=0
scale=1
for arg in "$@"; do
  case "$arg" in
    -r | --reset) reset=1 ;;
    -h | --help)
      sed -n '2,9p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    '' | *[!0-9]*)
      echo "不明な引数: $arg" >&2
      exit 1
      ;;
    *) scale=$arg ;;
  esac
done

if [ "$reset" = 1 ]; then
  echo "DB ボリュームを削除して作り直す。既存データは全て失われる。"
  docker compose down -v
  docker compose up -d db
else
  docker compose up -d db
fi

# seed は db に直接つなぐので、healthcheck が通るまで待つ。
printf 'db の起動を待つ'
for _ in $(seq 1 60); do
  if [ "$(docker inspect -f '{{.State.Health.Status}}' "$(docker compose ps -q db)" 2>/dev/null)" = healthy ]; then
    echo ' 完了'
    break
  fi
  printf .
  sleep 2
done

echo "scale=${scale} で投入する"
docker run --rm \
  -v "$(pwd):/app" \
  -v "${MODCACHE_VOLUME}:/go/pkg/mod" \
  -w /app \
  -e DATABASE_URL="sakuravel:password@tcp(db:3306)/sakuravel?parseTime=true&charset=utf8mb4" \
  --network "$NETWORK" \
  "$GO_IMAGE" go run ./seed/main.go -scale "$scale"
