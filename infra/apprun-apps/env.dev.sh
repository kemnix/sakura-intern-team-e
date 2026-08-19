# dev 環境の非秘密設定(コミット対象)。
# deploy-apps.sh と CI(migrate / スモークテスト)が source する。
# 「明示した環境変数が最優先」(VAR="${VAR:-既定値}")なので、一時的な差し替えは
# 環境変数で上書きできる。秘密(パスワード類)は絶対に書かない →
# ローカル: ../terraform-apprun/env.sh / CI: GitHub Secrets。
# ※インフラ由来の値(LB VIP / DB IP / 踏み台 IP)はここには置かない —
#   tfstate が唯一の情報源(jsonnet は --tfstate、CI は terraform output で導出)

export PUBLIC_FQDN="${PUBLIC_FQDN:-skrint-e-dev.kemnix.dev}"
export REGISTRY_HOST="${REGISTRY_HOST:-intern-team-e.sakuracr.jp}"
export REGISTRY_USER="${REGISTRY_USER:-cicd-push-user}"
export DB_USER="${DB_USER:-sakuravel_app}"
export DB_NAME="${DB_NAME:-sakuravel_app}"
export USE_LETS_ENCRYPT="${USE_LETS_ENCRYPT:-true}"
export SCALE_MIN="${SCALE_MIN:-1}" # dev はコスト優先で 1〜2
export SCALE_MAX="${SCALE_MAX:-2}"
