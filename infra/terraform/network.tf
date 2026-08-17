########################################
# スイッチ (vSwitch)
########################################

resource "sakura_vswitch" "private_net" {
  name = "intern2026-private-net-sw"
  zone = var.zone
}