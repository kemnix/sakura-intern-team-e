// frontend — proxy(nginx) からノード直（<ノードIP>:3000）でのみアクセスされる
local c = import 'common.libsonnet';
local p = c.params;

c.base {
  name: 'frontend',
  image: p.registryHost + '/sakuravel-frontend:' + p.tag,
  cpu: 500, // ミリコア
  memory: 512, // MiB
  env: [
    { key: 'API_URL', value: '/api', secret: false },
  ],
  exposedPorts: [
    {
      targetPort: 3000,
      healthCheck: c.healthCheck('/'),
    },
  ],
}
