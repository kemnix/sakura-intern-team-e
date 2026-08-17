# terraform.tf
# v3 registry: https://registry.terraform.io/providers/sacloud/sakura/latest

terraform {
  required_version = ">= 1.11"

  required_providers {
    sakura = {
      source  = "sacloud/sakura"
      version = "~> 3.8"
    }
  }
}

provider "sakura" {
  # SAKURA_ACCESS_TOKEN / SAKURA_ACCESS_TOKEN_SECRET の環境変数も利用可能
  token  = var.sakura_access_token
  secret = var.sakura_access_token_secret
  zone   = var.zone
}
