output "bastion_ip_address" {
  description = "踏み台サーバーのグローバル IP（CD の migration 実行で使用）"
  value       = sakura_server.bastion.ip_address
}

output "db_private_ip" {
  description = "DB アプライアンスのプライベート IP(CI の migrate と jsonnet の tfstate lookup が参照)"
  value       = var.db_private_ip
}

output "public_fqdn" {
  description = "公開 FQDN。唯一の情報源は tfvars の public_fqdn(jsonnet の tfstate lookup と CI のスモークテストが参照)"
  value       = var.public_fqdn
}

output "lb_private_vip" {
  description = "AppRun LB のプライベート VIP(nginx の折り返し先。jsonnet は当面 LB リソース属性を直接 lookup しているが、人間の確認用に output も出す)"
  value       = var.apprun_lb_private_ip
}

output "lb_node_addresses" {
  description = "LB ノードのアドレス一覧。グローバルIP → public_fqdn（<IP>.nip.io）、プライベートVIP → apprun_lb_private_ip の設定に使う"
  value = [
    for n in data.sakura_apprun_dedicated_lb_nodes.main.nodes : {
      status     = n.status
      interfaces = n.interfaces
    }
  ]
}

output "available_worker_service_classes" {
  description = "選択可能なワーカーノードのサービスクラス一覧（tfvars の apprun_worker_service_class_name に使う名前）"
  value       = data.sakura_apprun_dedicated_worker_service_classes.all.classes
}

output "available_lb_service_classes" {
  description = "選択可能な LB のサービスクラス一覧（tfvars の apprun_lb_service_class_name に使う名前）"
  value       = data.sakura_apprun_dedicated_lb_service_classes.all.classes
}

output "vswitch_id" {
  description = "プライベートネットワークの vSwitch ID（AppRun 専有型のクラスタ作成時にコンパネで指定する）"
  value       = sakura_vswitch.private_net.id
}
