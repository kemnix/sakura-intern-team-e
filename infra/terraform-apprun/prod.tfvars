# prod 環境の非秘密設定（コミット対象。秘密は書かない → env.sh / GitHub Secrets）
# 使い方: terraform workspace select prod && terraform plan -var-file=prod.tfvars

environment = "prod"

registry_name = "intern-team-e"

# 鍵認証のみ前提で全開放（理由は dev.tfvars 参照）
admin_allow_cidrs = ["0.0.0.0/0"]

bastion_ssh_public_key_path = "keys/bastion.pub"

########################################
# AppRun 専有型【要検証: 実値をコンパネ / API リファレンスで確認して置き換える】
########################################

apprun_service_principal_id = "TODO-console-で確認"
# サービスクラスは Terraform がデータソースで自動取得する（未指定なら一覧の先頭）。
# スペックを明示したい場合のみ、名前で指定する:
#   一覧の確認: terraform output available_worker_service_classes / available_lb_service_classes
apprun_worker_service_class_name = "AppRun専有型 ワーカ 4vCPU / 4GBメモリ"
apprun_lb_service_class_name     = "AppRun専有型 ロードバランサ 1vCPU / 2GBメモリ（冗長構成）"

# LB 作成後に実際のプライベート VIP を確認して合わせる（nginx の折り返し先）
# apprun_lb_private_ip = "192.168.1.120"

########################################
# 公開設定（ドメイン取得後に設定。シンプル監視もこの FQDN を監視する）
########################################

public_fqdn        = "" # 例: sakuravel.example.com
lets_encrypt_email = "" # 例: team-e@example.com
