# dev 環境の非秘密設定（コミット対象。秘密は書かない → env.sh / GitHub Secrets）
# 使い方: terraform workspace select dev && terraform plan -var-file=dev.tfvars

environment = "dev"
zone        = "is1c"

# コンテナレジストリ（<registry_name>.sakuracr.jp）
registry_name = "intern-team-e"

# 踏み台への SSH を許可する接続元（作業場所の IP。変わったらここを更新して apply）
admin_allow_cidrs = [
  "61.211.224.100/32",
  "124.37.189.194/32",
  "133.106.34.117/32",
]

# 踏み台の SSH 公開鍵（keys/README.md の手順で生成してコミット）
bastion_ssh_public_key_path = "keys/bastion.pub"

########################################
# AppRun 専有型【要検証: 実値をコンパネ / API リファレンスで確認して置き換える】
########################################

apprun_service_principal_id = "113801850061"
# サービスクラスは Terraform がデータソースで自動取得する（未指定なら一覧の先頭）。
# スペックを明示したい場合のみ、名前で指定する:
#   一覧の確認: terraform output available_worker_service_classes / available_lb_service_classes
apprun_worker_service_class_name = "AppRun専有型 ワーカ 4vCPU / 4GBメモリ"
apprun_lb_service_class_name     = "AppRun専有型 ロードバランサ 1vCPU / 2GBメモリ（冗長構成）"

monitoring_suite_log_endpoint = "113801924446.logs.monitoring.global.api.sacloud.jp"

# LB 作成後に実際のプライベート VIP を確認して合わせる（nginx の折り返し先）
# apprun_lb_private_ip = "192.168.1.120"

########################################
# 公開設定（dev はドメインなし運用。取得したら設定する）
########################################

public_fqdn        = "skrint-e-dev.kemnix.dev" # 例: dev.sakuravel.example.com
lets_encrypt_email = "tsubaki@nxla.net"        # 空なら TLS 自動発行なし
