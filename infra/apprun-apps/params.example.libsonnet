// アプリ定義に流し込むパラメータの雛形。
// 実際の params.libsonnet は deploy-apps.sh が環境変数から自動生成する（gitignore済み）。
// 手で jsonnet を評価したいときだけ、これをコピーして値を埋める:
//   cp params.example.libsonnet params.libsonnet
{
  env: 'dev', // dev / prod（クラスタ名 sakuravel-<env> の解決に使う）
  tag: 'latest', // イメージタグ（CD は git sha を渡す）
  fqdn: 'dev.example.com', // 公開 FQDN
  // LB VIP / DB IP はここには無い（tfstate lookup。common.libsonnet 参照）
  registryHost: 'intern-team-e.sakuracr.jp',
  registryUser: 'cicd-push-user',
  registryPassword: 'CHANGEME', // 秘密。params.libsonnet はコミットしないこと
  dbUser: 'sakuravel_app',
  dbPassword: 'CHANGEME', // 秘密
  dbName: 'sakuravel_app',
  useLetsEncrypt: true,
  scaleMin: 2,
  scaleMax: 5,
}
