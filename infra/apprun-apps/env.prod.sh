# prod 環境の非秘密設定(コミット対象)。詳細なルールは env.dev.sh のコメント参照。

# 本番ドメイン取得後に設定する(未設定のままだと deploy-apps.sh が明示エラーで止まる)
export PUBLIC_FQDN="${PUBLIC_FQDN:-}"
export REGISTRY_HOST="${REGISTRY_HOST:-intern-team-e.sakuracr.jp}"
export REGISTRY_USER="${REGISTRY_USER:-cicd-push-user}"
export DB_USER="${DB_USER:-sakuravel_app}"
export DB_NAME="${DB_NAME:-sakuravel_app}"
export USE_LETS_ENCRYPT="${USE_LETS_ENCRYPT:-true}"
export SCALE_MIN="${SCALE_MIN:-2}"
export SCALE_MAX="${SCALE_MAX:-5}"
