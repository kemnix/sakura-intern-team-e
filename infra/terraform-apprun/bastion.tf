########################################
# 踏み台サーバー（DB 運用作業・マイグレーション実行用）
########################################
# 役割:
#   - DB アプライアンスへの閉域アクセス経路（mysql クライアント / マイグレーション）
#   - CD からの migration 実行の中継点（deploy.yml が SSH で使用）
# アプリは動かさない。AppRun 移行後、常時必要でなければ停止しておいてもよい。

data "sakura_archive" "ubuntu" {
  name = "Ubuntu Server 24.04.2 LTS 64bit (cloudimg)"
  zone = var.zone
}

resource "sakura_disk" "bastion" {
  name              = "sakuravel-${var.environment}-bastion-disk"
  plan              = "ssd"
  size              = 20
  source_archive_id = data.sakura_archive.ubuntu.id
}

resource "sakura_server" "bastion" {
  name   = "sakuravel-${var.environment}-bastion"
  disks  = [sakura_disk.bastion.id]
  core   = 1
  memory = 1

  network_interface = [
    {
      upstream         = "shared"
      packet_filter_id = sakura_packet_filter.bastion.id
    },
    { upstream = sakura_vswitch.private_net.id },
  ]

  user_data = templatefile("${path.module}/cloud-init-bastion.yaml.tftpl", {
    hostname        = "sakuravel-${var.environment}-bastion"
    password        = var.bastion_password
    ssh_public_key  = file(pathexpand(var.bastion_ssh_public_key_path))
    private_ip_cidr = var.bastion_private_ip_cidr
  })
}
