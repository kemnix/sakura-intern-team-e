// nginx リバースプロキシ — 公開の入口（443）。/api を api へ、その他を frontend へ
// ワーカーノード直（upstream に node IP:port を列挙）で振り分ける
local c = import 'common.libsonnet';
local p = c.params;

c.base {
  name: 'proxy',
  image: p.registryHost + '/sakuravel-proxy:' + p.tag,
  cpu: 250, // ミリコア
  memory: 256, // MiB
  env: [
    { key: 'API_UPSTREAM_SERVERS', value: c.upstreamServers(8080), secret: false },
    { key: 'FRONT_UPSTREAM_SERVERS', value: c.upstreamServers(3000), secret: false },
  ],
  exposedPorts: [
    {
      // AppRun はホストNW共有のため targetPort はクラスタ内で一意にする（api=8080 と衝突し exit 1 になる）
      targetPort: 8081,
      loadBalancerPort: 443,
      useLetsEncrypt: p.useLetsEncrypt,
      host: [c.fqdn], // tfvars の public_fqdn(tfstate lookup)
      healthCheck: c.healthCheck('/nginx-healthz'),
    },
  ],
}
