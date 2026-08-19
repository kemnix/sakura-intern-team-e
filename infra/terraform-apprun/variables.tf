########################################
# 環境・ゾーン
########################################

variable "environment" {
  description = "環境名 (prod / dev)。terraform workspace か tfvars で切り替える。"
  type        = string
  default     = "dev"

  validation {
    condition     = contains(["prod", "dev"], var.environment)
    error_message = "environment は prod か dev を指定してください。"
  }
}

variable "zone" {
  description = "リソースを作成するゾーン"
  type        = string
  default     = "is1c"
}

########################################
# ネットワーク
########################################

variable "private_net_cidr" {
  description = "プライベートネットワークの CIDR"
  type        = string
  default     = "192.168.1.0/24"
}

variable "db_private_ip" {
  description = "DBアプライアンスのプライベート IP"
  type        = string
  default     = "192.168.1.30"
}

variable "bastion_private_ip_cidr" {
  description = "踏み台サーバーのプライベート NIC に振る IP (CIDR 形式)"
  type        = string
  default     = "192.168.1.40/24"
}

variable "admin_allow_cidrs" {
  description = "踏み台への SSH を許可する接続元 CIDR のリスト（オフィス/自宅 IP など）"
  type        = list(string)
  # デフォルトは全拒否に近い値。必ず実際の IP に上書きすること。
  default = []
}

########################################
# DB アプライアンス
########################################

variable "db_username" {
  description = "データベースのユーザー名"
  type        = string
  default     = "sakuravel_app"
}

variable "db_password" {
  description = "データベースのパスワード"
  type        = string
  sensitive   = true
}

variable "db_name" {
  description = "作成するデータベース名"
  type        = string
  default     = "sakuravel_app"
}

variable "db_plan" {
  description = "DBアプライアンスのプラン。dev はコスト削減のため最小プランを推奨。"
  type        = string
  default     = "10g"
}

########################################
# 踏み台サーバー
########################################

variable "bastion_password" {
  description = "踏み台サーバーの初期パスワード（SSH はパスワード認証無効。コンパネ用）"
  type        = string
  sensitive   = true
}

variable "bastion_ssh_public_key_path" {
  description = "踏み台への SSH 公開鍵ファイルのパス。CI でも解決できるようリポジトリ内のファイル（例: keys/bastion.pub）を推奨"
  type        = string
  default     = "keys/bastion.pub"
}

########################################
# コンテナレジストリ
########################################

variable "registry_name" {
  description = "コンテナレジストリ名（<registry_name>.sakuracr.jp になる）"
  type        = string
  default     = "intern-team-e"
}

variable "registry_user" {
  description = "レジストリのユーザー名（CI の push / AppRun の pull で使用）"
  type        = string
  default     = "cicd-push-user"
}

variable "registry_password" {
  description = "レジストリユーザーのパスワード"
  type        = string
  sensitive   = true
}

########################################
# AppRun 専有型
########################################

variable "apprun_service_principal_id" {
  description = "AppRun専有型のサービスプリンシパルID。【要検証】コンパネの AppRun専有型 画面で確認して設定する。"
  type        = string
}

# サービスクラスはデータソースで自動取得する（apprun.tf 参照）。
# 特定のスペックを使いたい場合のみ、クラス「名」で指定する。
# 利用可能な名前の一覧は apply 後に:
#   terraform output available_worker_service_classes
#   terraform output available_lb_service_classes

variable "apprun_worker_service_class_name" {
  description = "ワーカーノードのサービスクラス名。空なら一覧の先頭クラスを使う"
  type        = string
  default     = ""
}

variable "apprun_lb_service_class_name" {
  description = "AppRun LB のサービスクラス名。空なら一覧の先頭クラスを使う"
  type        = string
  default     = ""
}

variable "apprun_worker_ip_pool_start" {
  description = "ワーカーノードに割り当てるプライベート IP プールの開始"
  type        = string
  default     = "192.168.1.100"
}

variable "apprun_worker_ip_pool_end" {
  description = "ワーカーノードに割り当てるプライベート IP プールの終了"
  type        = string
  default     = "192.168.1.119"
}

variable "apprun_lb_ip_pool_start" {
  description = "AppRun LB に割り当てるプライベート IP プールの開始"
  type        = string
  default     = "192.168.1.120"
}

variable "apprun_lb_ip_pool_end" {
  description = "AppRun LB に割り当てるプライベート IP プールの終了"
  type        = string
  default     = "192.168.1.123"
}

variable "seg_private_ip" {
  description = "SEG（サービスエンドポイントゲートウェイ）に割り当てたプライベート IP。コンパネで SEG 有効化時に同じ値を入力すること"
  type        = string
  default     = "192.168.1.3"
}

variable "name_servers" {
  description = "ワーカーノード・LB のネームサーバ。閉域 vSwitch では DNS は SEG が提供するため、SEG の IP を指定する（公開 DNS は閉域から到達できない）"
  type        = list(string)
  default     = [] # 空なら [seg_private_ip] を使う
}

variable "lets_encrypt_email" {
  description = "Let's Encrypt 証明書発行の通知先メールアドレス（空なら TLS 自動発行なし）"
  type        = string
  default     = ""
}

variable "public_fqdn" {
  description = "公開 FQDN（単一オリジン）。nginx が受けて /api を api へ、それ以外を frontend へ振り分ける"
  type        = string
  default     = "" # 例: sakuravel.example.com
}

variable "apprun_lb_private_ip" {
  description = "AppRun LB のプライベート側 VIP（ip_pool 120-123 の外の空きアドレス。nginx はノード直結のため必須参照は無く、プライベート側の疎通確認用）"
  type        = string
  default     = "192.168.1.124"
}

variable "monitoring_suite_log_endpoint" {
  description = "モニタリングスイートのログストレージのエンドポイント（SEG 経由でコンテナログを転送する）。ログストレージ詳細画面で確認して設定する。空なら連携なし"
  type        = string
  default     = ""
}

variable "alert_slack_webhook" {
  description = "ダウン検知時の通知先 Slack Incoming Webhook URL"
  type        = string
  sensitive   = true
  default     = ""
}
