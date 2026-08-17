########################################
# データベースアプライアンス: intern2026-db
########################################
# 冗長化・レプリケーションなし → replica_* は指定しない。
# クローン元なし → 新規作成。
# database_version は MariaDB = "10.11" (プロバイダー例・sacloud API が採用する現行版)。

resource "sakura_database" "db" {
  name = "intern2026-db"
  zone = var.zone

  database_type    = "mariadb"
  database_version = "10.11"
  plan             = "10g" # 10GB

  username            = var.db_username
  password_wo         = var.db_password
  password_wo_version = 1

  network_interface = {
    vswitch_id    = sakura_vswitch.private_net.id
    ip_address    = element(split("/", var.db_private_net_cidr), 0)
    netmask       = tonumber(element(split("/", var.db_private_net_cidr), 1))
    gateway       = var.db_private_net_gateway
    source_ranges = [var.db_private_net_allow_cidr]
  }

  # 定期バックアップ: 毎日 03:00
  backup = {
    days_of_week = ["sun", "mon", "tue", "wed", "thu", "fri", "sat"]
    time         = "03:00"
  }

  # モニタリングスイート: 連携する
  monitoring_suite = {
    enabled = true
  }
}
