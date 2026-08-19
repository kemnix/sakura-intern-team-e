# AppRun専有型ベース構成の Terraform ルート
#
# 管理境界:
#   Terraform 管理     : AppRun専有型（クラスタ/ASG/LB/アプリ）/ vSwitch / SEG /
#                        DBアプライアンス / 踏み台 / パケットフィルタ / コンテナレジストリ / シンプル監視
#   Terraform 管理外   : オブジェクトストレージのバケット作成のみ

terraform {
  required_version = ">= 1.11"

  required_providers {
    sakura = {
      source  = "sacloud/sakura"
      version = "~> 3.8"
    }
  }

  # tfstate はさくらのオブジェクトストレージ（S3互換）に保存する。
  # バケットは事前にコンパネで作成。アクセスキーは ~/.aws/credentials の
  # [sakura] プロファイルから読む（CD ではランナー上に同名プロファイルを生成する。
  # deploy.yml の "Set up object storage credentials profile" ステップ参照）。
  # ※bucket / endpoint を変える場合は deploy-apps.sh の TFSTATE_URL / AWS_* デフォルトも揃えること
  backend "s3" {
    bucket  = "sakuravel-app-tf"
    profile = "sakura"
    key     = "apprun/terraform.tfstate"
    region  = "jp-north-1" # isk01(石狩第1サイト)のリージョン。東京(tky01)は jp-east-1
    endpoints = {
      s3 = "https://s3.isk01.sakurastorage.jp"
    }

    # S3互換ストレージ向けの互換フラグ
    skip_credentials_validation = true
    skip_region_validation      = true
    skip_requesting_account_id  = true
    skip_metadata_api_check     = true
    use_path_style              = true
    # TF 1.11+ が送る AWS 版チェックサムを S3 互換ストレージが受理しない問題の回避
    # (hashicorp/terraform#37203)
    skip_s3_checksum = true

    # 【要検証】さくらのオブジェクトストレージが条件付き書き込みに対応していれば
    # use_lockfile = true でネイティブロックが使える。未対応の場合は
    # GitHub Actions 側の concurrency でロックを代替する（deploy.yml 参照）。
    # use_lockfile = true
  }
}

provider "sakura" {
  # SAKURA_ACCESS_TOKEN / SAKURA_ACCESS_TOKEN_SECRET の環境変数で供給
  zone = var.zone
}
