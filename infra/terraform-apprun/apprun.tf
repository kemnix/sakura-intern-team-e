########################################
# AppRun 専有型
########################################
# クラスタ → オートスケーリンググループ（ワーカーノード）→ LB → アプリケーション → バージョン
# の順で依存する。
#
# ルーティング設計（単一オリジン方式・ワーカーノード直結）:
#   公開ホストは nginx(proxy) が一手に受け、/api を api へ、それ以外を frontend へ振り分ける。
#   AppRun はコンテナをホストNW共有で実行するため、上流は <ノードIP>:<ポート> へ直接届く
#   （ノードIPリストは jsonnet が tfstate の ip_pool から生成）。
#
#     ユーザー → LB:443 (host: <public_fqdn>) → nginx(ノード:8081)
#                                                ├─ /api → 各ノードIP:8080 (api)
#                                                └─ /    → 各ノードIP:3000 (frontend)
#
#   api / frontend は LB に公開ルートを持たない（jsonnet で loadBalancerPort を付けない）。
#
# デプロイの仕組み:
#   Terraform はクラスタ/ASG/LB までの「箱」を管理し、アプリケーションとバージョンは
#   apprun-dedicated-cli が管理する（このファイル末尾のコメント参照）。

# 利用可能なサービスクラス（スペック）の一覧を API から取得する
data "sakura_apprun_dedicated_worker_service_classes" "all" {}
data "sakura_apprun_dedicated_lb_service_classes" "all" {}

locals {
  # 環境ごとのワーカーノード台数
  node_count = { prod = { min = 2, max = 5 }, dev = { min = 2, max = 5 } }

  # ネームサーバ: 閉域 vSwitch では SEG が DNS を提供する（公式記事の構成に準拠）
  name_servers = length(var.name_servers) > 0 ? var.name_servers : [var.seg_private_ip]

  # サービスクラスの解決: 変数で「名前」を指定していればそれを、空なら一覧の先頭を使う。
  # 一覧は outputs（available_*_service_classes）で確認できる。
  worker_service_class_path = var.apprun_worker_service_class_name != "" ? one([
    for c in data.sakura_apprun_dedicated_worker_service_classes.all.classes : c.path
    if c.name == var.apprun_worker_service_class_name
  ]) : data.sakura_apprun_dedicated_worker_service_classes.all.classes[0].path

  lb_service_class_path = var.apprun_lb_service_class_name != "" ? one([
    for c in data.sakura_apprun_dedicated_lb_service_classes.all.classes : c.path
    if c.name == var.apprun_lb_service_class_name
  ]) : data.sakura_apprun_dedicated_lb_service_classes.all.classes[0].path
}

resource "sakura_apprun_dedicated_cluster" "main" {
  name                 = "sakuravel-${var.environment}"
  service_principal_id = var.apprun_service_principal_id
  lets_encrypt_email   = var.lets_encrypt_email != "" ? var.lets_encrypt_email : null

  ports = [
    { port = 443, protocol = "https" }, # 公開（nginx）
    # Let's Encrypt の検証用に開放必須（公式ドキュメント要件）。
    # ルーティング先のアプリは存在しないため、外から叩いても何にも到達しない
    { port = 80, protocol = "http" },
  ]
}

resource "sakura_apprun_dedicated_auto_scaling_group" "main" {
  cluster_id = sakura_apprun_dedicated_cluster.main.id
  name       = "sakuravel-${var.environment}-asg"
  zone       = var.zone

  min_nodes                 = local.node_count[var.environment].min
  max_nodes                 = local.node_count[var.environment].max
  worker_service_class_path = local.worker_service_class_path

  # ワーカーノードは SEG 経由でレジストリ/コントロールプレーンに接続するため、
  # SEG の作成完了を待ってから起動させる
  depends_on = [sakura_seg.main]

  interfaces = [
    {
      interface_index = 0
      upstream        = sakura_vswitch.private_net.id
      connects_to_lb  = true
      netmask         = tonumber(element(split("/", var.private_net_cidr), 1))
      default_gateway = cidrhost(var.private_net_cidr, 1)
      ip_pool = [
        { start = var.apprun_worker_ip_pool_start, end = var.apprun_worker_ip_pool_end },
      ]
    },
  ]

  name_servers = local.name_servers
}

resource "sakura_apprun_dedicated_lb" "main" {
  cluster_id            = sakura_apprun_dedicated_cluster.main.id
  auto_scaling_group_id = sakura_apprun_dedicated_auto_scaling_group.main.id
  name                  = "sakuravel-${var.environment}-lb"
  service_class_path    = local.lb_service_class_path

  # 公式構成: eth0 = 共有セグメント（グローバルIP自動割り当て = 公開の入口）、
  #           eth1 = SEG有効化済み vSwitch（ワーカーノードへの接続）
  interfaces = [
    {
      interface_index = 0
      upstream        = "shared"
    },
    {
      interface_index = 1
      upstream        = sakura_vswitch.private_net.id
      netmask         = tonumber(element(split("/", var.private_net_cidr), 1))
      # デフォルトルートは eth0（共有セグメント）側が持つため gateway は指定しない
      ip_pool = [
        { start = var.apprun_lb_ip_pool_start, end = var.apprun_lb_ip_pool_end },
      ]
      # 冗長構成2ノードの仮想IP（VRRP。実機で動作確認済み）。
      # nginx はノード直結のため現構成に必須の参照先は無いが、
      # プライベート側からの LB 疎通確認に使えるため維持している
      vip               = var.apprun_lb_private_ip
      virtual_router_id = 1
    },
  ]

  name_servers = local.name_servers
}

# LB ノードのアドレス確認用（apply 後に terraform output lb_node_addresses で参照）。
# グローバルIP（public_fqdn の DNS レコードを向ける先）をコンパネを開かずに確認できる。
data "sakura_apprun_dedicated_lb_nodes" "main" {
  cluster_id            = sakura_apprun_dedicated_cluster.main.id
  auto_scaling_group_id = sakura_apprun_dedicated_auto_scaling_group.main.id
  lb_id                 = sakura_apprun_dedicated_lb.main.id
}

########################################
# アプリケーション / バージョン は Terraform 管理外
########################################
# AppRun のバージョンは「作成 → activate → 旧版削除」というライフサイクルを持ち、
# 「active なバージョンは削除できない」制約が Terraform の replace（削除→作成）と
# 根本的に相性が悪いため、apprun-dedicated-cli で管理する。
#   定義: ../apprun-apps/*.jsonnet
#   デプロイ: ../apprun-apps/deploy-apps.sh（CD は deploy.yml が実行）
#   ロールバック: apprun-dedicated-cli rollback --app <app>.jsonnet
