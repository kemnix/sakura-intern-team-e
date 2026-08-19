########################################
# コンテナレジストリ
########################################
# チームで既にコンパネから作成済みの場合は、新規作成せず import する:
#   terraform import sakura_container_registry.main[0] <レジストリのID>
# レジストリは prod / dev で共用（イメージはタグで区別）のため、
# prod 側の apply でのみ作成する。

resource "sakura_container_registry" "main" {
  count = var.environment == "prod" ? 1 : 0

  name            = var.registry_name
  subdomain_label = var.registry_name
  # access_level は 2026-05 に公開アクセス設定が廃止され deprecated（常に認証必須）

  user = [
    {
      name                = var.registry_user
      password_wo         = var.registry_password
      password_wo_version = 1
      permission          = "readwrite" # CI の push 用。AppRun の pull 用に readonly ユーザーを分けてもよい
    },
  ]
}

########################################
# シンプル監視
########################################
# AppRun 専有型の公開エンドポイントを外形監視し、ダウン時に Slack へ通知する。
# 監視対象の FQDN は変数で受け取る（未設定なら作成しない）。

resource "sakura_simple_monitor" "apprun" {
  count = var.public_fqdn != "" ? 1 : 0

  target     = var.public_fqdn
  delay_loop = 60 # 監視間隔（秒）

  health_check = {
    protocol = "https"
    # nginx 経由で api の /healthz まで到達するパス（LB → nginx → LB → api の全経路を検証できる）
    path   = "/api/healthz" # 【依存】アプリ側に /healthz 実装が必要（未実装なら "/" で代替）
    status = 200
  }

  max_check_attempts   = 3
  notify_email_enabled = false
  notify_slack_enabled = var.alert_slack_webhook != ""
  notify_slack_webhook = var.alert_slack_webhook

  monitoring_suite = {
    enabled = true
  }
}
