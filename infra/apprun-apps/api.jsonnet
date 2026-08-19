// Go API — proxy(nginx) からノード直（<ノードIP>:8080）でのみアクセスされる
local c = import 'common.libsonnet';
local p = c.params;

c.base {
  name: 'api',
  image: p.registryHost + '/sakuravel-backend:' + p.tag,
  cpu: 1000, // ミリコア（1000 = 1コア）
  memory: 1024, // MiB
  env: [
    { key: 'COOKIE_SECURE', value: 'true', secret: false },
    { key: 'DATABASE_URL', value: c.databaseUrl, secret: true },
    { key: 'PORT', value: '8080', secret: false },
  ],
  exposedPorts: [
    {
      targetPort: 8080,
      healthCheck: c.healthCheck('/healthz'),
    },
  ],
}
