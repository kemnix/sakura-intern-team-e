########################################
# プライベートネットワーク（vSwitch）
########################################
# AppRun専有型のワーカーノード・DBアプライアンス・踏み台をつなぐ閉域網。
#
# 【手作業が必要】SEG（サービスエンドポイントゲートウェイ）の有効化は
# 現時点で Terraform リソースが無いため、vSwitch 作成後にコンパネで実施する:
#   1. この vSwitch に対して SEG を有効化
#   2. 接続先にコンテナレジストリを指定
#   3. AppRun専有型コントロールプレーンの Managed オプションを有効化
# （手順: https://manual.sakura.ad.jp/cloud/apprun-dedicated/tips.html）

resource "sakura_vswitch" "private_net" {
  name = "sakuravel-${var.environment}-private-net"
  zone = var.zone
}

########################################
# SEG（サービスエンドポイントゲートウェイ）
########################################
# 閉域の vSwitch からコンテナレジストリ・AppRun コントロールプレーンへ接続する窓口。
# DNS も SEG が提供する（ワーカー/LB の name_servers は seg_private_ip を指す）。
# ※ コンパネで手動有効化済みの場合は二重作成になるため、
#    手動分を削除するか terraform import sakura_seg.main <ID> で取り込むこと。

resource "sakura_seg" "main" {
  zone       = var.zone
  vswitch_id = sakura_vswitch.private_net.id

  server_ip_addresses = [var.seg_private_ip]
  netmask             = tonumber(element(split("/", var.private_net_cidr), 1))

  # モニタリングスイート連携: コンテナの標準出力/エラーをログストレージへ転送する閉域経路。
  # 有効化には SEG 接続に加え、AppRun専有型コンパネの「ログ・メトリクス設定」で
  # エージェントログ/コンテナログを「利用する」に設定する必要がある（手作業）。
  monitoring_suite_enabled = var.monitoring_suite_log_endpoint != ""

  endpoint_setting = {
    # AppRun専有型コントロールプレーン（Managed オプション）
    apprun_dedicated_control_enabled = true
    # 接続先レジストリ（FQDN 指定で正しいことをコンパネ表示で確認済み）
    container_registry_endpoints = ["${var.registry_name}.sakuracr.jp"]
    # オブジェクトストレージ（tfstate バケットのある石狩第1サイト。terraform.tf の backend と揃える）
    object_storage_endpoints = ["s3.isk01.sakurastorage.jp"]
    # モニタリングスイートのログストレージ（未設定なら接続しない）
    monitoring_suite_endpoints = var.monitoring_suite_log_endpoint != "" ? [var.monitoring_suite_log_endpoint] : null
  }
}

########################################
# パケットフィルタ（踏み台用）
########################################
# さくらのパケットフィルタはステートレスなので、
# 「外向き通信の戻りパケット」も明示的に許可する必要がある。

resource "sakura_packet_filter" "bastion" {
  name        = "sakuravel-${var.environment}-bastion-pf"
  description = "bastion: allow SSH from admin CIDRs only"
  zone        = var.zone
}

resource "sakura_packet_filter_rules" "bastion" {
  zone             = var.zone
  packet_filter_id = sakura_packet_filter.bastion.id

  expression = concat(
    # SSH: 許可した接続元 CIDR からのみ
    # （さくらのパケットフィルタはマスク長 /32 を受け付けないため、
    #   単一IPは "x.x.x.x" のみの表記に変換して送る）
    [
      for cidr in var.admin_allow_cidrs : {
        protocol         = "tcp"
        source_network   = replace(cidr, "/32", "")
        destination_port = "22"
        allow            = true
        description      = "ssh from admin"
      }
    ],
    [
      # apt / HTTPS 等、踏み台発の通信の戻りパケット
      {
        protocol    = "tcp"
        source_port = "443"
        allow       = true
        description = "return packets of outbound https"
      },
      {
        protocol    = "tcp"
        source_port = "80"
        allow       = true
        description = "return packets of outbound http (apt)"
      },
      # DHCP 応答（共有セグメントのグローバル IP は DHCP で付与されるため必須。
      #   これを許可しないと OFFER/ACK が deny all で落ち、IP を取得できない）
      {
        protocol    = "udp"
        source_port = "67"
        allow       = true
        description = "dhcp reply"
      },
      # DNS / NTP の戻り
      {
        protocol    = "udp"
        source_port = "53"
        allow       = true
        description = "dns reply"
      },
      {
        protocol    = "udp"
        source_port = "123"
        allow       = true
        description = "ntp reply"
      },
      # ICMP（疎通確認用）
      {
        protocol    = "icmp"
        allow       = true
        description = "icmp"
      },
      # 上記以外の inbound はすべて拒否
      {
        protocol    = "ip"
        allow       = false
        description = "deny all"
      },
    ]
  )
}
