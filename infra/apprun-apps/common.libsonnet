// 3アプリ共通の設定（フィールド名は AppRun専有型 API と 1:1 対応）
local p = import 'params.libsonnet';

// インフラ由来の値は tfstate を唯一の情報源とする（env ファイル等への転記は不要）。
// deploy 時は deploy-apps.sh が --tfstate を渡す。CI の render はオフラインで
// native 関数が登録されないため、ダミー値（TEST-NET）でフォールバックする。
local tf = std.native('tfstate');
local tfstate(path, dummy) = if tf == null then dummy else tf(path);

// nginx の上流 = ワーカーノード直（ホストNW共有のため node IP:port で届く）。
// ノード IP は ip_pool の先頭から max_nodes 台分と仮定する（実測で先頭から連番割当）。
// LB:80 の internal ラベル折り返しは廃止済み — Host ヘッダ偽装で外部から内部アプリへ
// 到達できる穴を塞ぐため（80 番自体は Let's Encrypt の検証用に開けたまま）。
local poolStart = tfstate('sakura_apprun_dedicated_auto_scaling_group.main.interfaces[0].ip_pool[0].start', '192.0.2.10');
local maxNodes = tfstate('sakura_apprun_dedicated_auto_scaling_group.main.max_nodes', 2);
local sp = std.split(poolStart, '.');
local nodeIPs = [
  std.join('.', [sp[0], sp[1], sp[2]]) + '.' + std.toString(std.parseInt(sp[3]) + i)
  for i in std.range(0, maxNodes - 1)
];

{
  params:: p,

  // nginx の upstream ブロックに入れる server 行（未割当 IP は max_fails で即座に切り離される）
  upstreamServers(port)::
    std.join(' ', ['server %s:%d max_fails=1 fail_timeout=10s;' % [ip, port] for ip in nodeIPs]),

  // DB アプライアンスのプライベート IP
  dbHost:: tfstate('output.db_private_ip', '192.0.2.2'),

  // 公開 FQDN(証明書のホスト名)。唯一の情報源は tfvars の public_fqdn。
  // 初回は terraform apply で output が state に載ってから deploy すること(CD はその順で実行される)
  fqdn::
    local v = tfstate('output.public_fqdn', 'dev.example.com');
    assert std.length(v) > 0 : 'tfvars の public_fqdn が未設定です(シンプル監視と証明書の両方に必要)';
    v,
  // Go の MySQL DSN（ホストだけ tfstate 由来、資格情報は params 経由）
  databaseUrl:: p.dbUser + ':' + p.dbPassword + '@tcp(' + $.dbHost + ':3306)/' + p.dbName
                + '?parseTime=true&charset=utf8mb4',

  base:: {
    cluster: 'sakuravel-' + p.env,
    scalingMode: 'cpu',
    minScale: p.scaleMin,
    maxScale: p.scaleMax,
    scaleOutThreshold: 70,
    scaleInThreshold: 30, // API の許容範囲は 30〜70
    registryUsername: p.registryUser,
    registryPassword: p.registryPassword,
  },

  healthCheck(path):: {
    path: path,
    intervalSeconds: 10,
    timeoutSeconds: 5,
  },
}
