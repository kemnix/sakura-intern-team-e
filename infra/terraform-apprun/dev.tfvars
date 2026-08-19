# dev 環境の非秘密設定（コミット対象。秘密は書かない → env.sh / GitHub Secrets）
# 使い方: terraform workspace select dev && terraform plan -var-file=dev.tfvars

environment = "dev"
zone        = "is1c"

# コンテナレジストリ（<registry_name>.sakuracr.jp）
registry_name = "intern-team-e"

# 踏み台 SSH の接続元制限。鍵認証のみ（パスワード認証無効）を前提に全開放。
# CD（GitHub ホステッドランナー）の IP が巨大かつ毎週変わり列挙不能なため。
# 絞りたくなったら CIDR を列挙する（その場合 CD の migrate はランナーIPの動的許可が必要）
admin_allow_cidrs = ["0.0.0.0/0"]

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
