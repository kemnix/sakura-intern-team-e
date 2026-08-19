########################################
# データベースアプライアンス
########################################
# prod / dev で同一定義・別インスタンス（environment ごとに workspace / tfvars を分けて apply）。
# dev はコスト削減のため db_plan を最小の "10g" のままにしておく。

resource "sakura_database" "db" {
  name = "sakuravel-${var.environment}-db"
  zone = var.zone

  database_type    = "mariadb"
  database_version = "10.11"
  plan             = var.db_plan

  username            = var.db_username
  password_wo         = var.db_password
  password_wo_version = 1

  network_interface = {
    vswitch_id = sakura_vswitch.private_net.id
    ip_address = var.db_private_ip
    port       = 3306
    netmask    = tonumber(element(split("/", var.private_net_cidr), 1))
    gateway    = cidrhost(var.private_net_cidr, 1)
    # AppRun ワーカーノードと踏み台が属するプライベート網からのみ許可
    source_ranges = [var.private_net_cidr]
  }

  # 定期バックアップ: 毎日 03:00（restore リハーサルを運用手順書に含めること）
  backup = {
    days_of_week = ["sun", "mon", "tue", "wed", "thu", "fri", "sat"]
    time         = "03:00"
  }

  monitoring_suite = {
    enabled = true
  }
}
