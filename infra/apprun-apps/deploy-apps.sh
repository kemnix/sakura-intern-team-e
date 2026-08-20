#!/usr/bin/env bash
# AppRun専有型へ 3 アプリ（api / frontend / proxy）をデプロイする。
# apprun-dedicated-cli の deploy は「新バージョン作成 → 有効化 → 起動待ち」まで冪等に行う。
#
# 使い方:
#   ローカル: source ../terraform-apprun/env.sh   # 秘密(パスワード類)
#             ENVIRONMENT=dev IMAGE_TAG=<git sha> ./deploy-apps.sh
#   CD      : deploy.yml が実行（秘密は Secrets、非秘密は env.<環境>.sh から注入）
#
# 前提: apprun-dedicated-cli がインストール済み
#   brew install fujiwara/tap/apprun-dedicated-cli
set -euo pipefail
cd "$(dirname "$0")"

: "${SAKURA_ACCESS_TOKEN:?SAKURA_ACCESS_TOKEN が未設定です}"
: "${SAKURA_ACCESS_TOKEN_SECRET:?SAKURA_ACCESS_TOKEN_SECRET が未設定です}"

ENVIRONMENT="${ENVIRONMENT:-dev}"

# 非秘密の環境別設定はコミット済みの env.<環境>.sh に集約（明示した環境変数が最優先）。
# 秘密はここには置かない → ローカル: ../terraform-apprun/env.sh / CI: GitHub Secrets
source "./env.${ENVIRONMENT}.sh"

IMAGE_TAG="${IMAGE_TAG:?IMAGE_TAG を設定してください (git SHA 推奨。latest は再pull保証が無くロールバックも壊れるため禁止)}"
REGISTRY_PASSWORD="${REGISTRY_PASSWORD:-${TF_VAR_registry_password:?REGISTRY_PASSWORD が未設定です}}"
DB_PASSWORD="${DB_PASSWORD:-${TF_VAR_db_password:?DB_PASSWORD が未設定です}}"

# インフラ由来の値(ノードIPプール / DB IP / FQDN)は tfstate から jsonnet が直接引く(--tfstate)。
# tfstate の接続情報(バケット/リージョン/エンドポイント)の唯一の情報源は
# terraform-apprun/backend.hcl — ここから読むことで二重管理を避ける
BACKEND_HCL="../terraform-apprun/backend.hcl"
TFSTATE_BUCKET="${TFSTATE_BUCKET:-$(awk -F'"' '/^bucket/{print $2}' "$BACKEND_HCL")}"
TFSTATE_URL="${TFSTATE_URL:-s3://${TFSTATE_BUCKET}/env:/${ENVIRONMENT}/apprun/terraform.tfstate}"
export AWS_PROFILE="${AWS_PROFILE:-sakura}"
export AWS_REGION="${AWS_REGION:-$(awk -F'"' '/^region/{print $2}' "$BACKEND_HCL")}"
export AWS_ENDPOINT_URL_S3="${AWS_ENDPOINT_URL_S3:-$(awk -F'"' '/^[[:space:]]*s3[[:space:]]*=/{print $2}' "$BACKEND_HCL")}"
# S3互換ストレージ対策の保険: SDK の整合性チェックを「必要な場合のみ」に抑える(tfstate-lookup も同じ SDK)
export AWS_REQUEST_CHECKSUM_CALCULATION="${AWS_REQUEST_CHECKSUM_CALCULATION:-when_required}"
export AWS_RESPONSE_CHECKSUM_VALIDATION="${AWS_RESPONSE_CHECKSUM_VALIDATION:-when_required}"

# パラメータを Jsonnet に渡す（params.libsonnet は gitignore 済み・毎回生成）
# 注意: パスワードに ' や \ が含まれると Jsonnet 文字列が壊れるため使用しないこと
cat > params.libsonnet <<EOF
{
  env: '${ENVIRONMENT}',
  tag: '${IMAGE_TAG}',
  registryHost: '${REGISTRY_HOST}',
  registryUser: '${REGISTRY_USER}',
  registryPassword: '${REGISTRY_PASSWORD}',
  dbUser: '${DB_USER}',
  dbPassword: '${DB_PASSWORD}',
  dbName: '${DB_NAME}',
  useLetsEncrypt: ${USE_LETS_ENCRYPT},
  scaleMin: ${SCALE_MIN},
  scaleMax: ${SCALE_MAX},
}
EOF
trap 'rm -f params.libsonnet' EXIT

# api / frontend を先に、公開入口の proxy を最後に
for app in api frontend proxy; do
  echo "==== deploy: ${app} (${ENVIRONMENT}, tag=${IMAGE_TAG}) ===="
  apprun-dedicated-cli deploy --app "${app}.jsonnet" --tfstate "${TFSTATE_URL}"
done

echo "all applications deployed."
