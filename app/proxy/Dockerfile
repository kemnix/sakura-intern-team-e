# 非root実行対応版の公式 nginx（AppRun はコンテナを非rootで実行するため通常版は
# /var/cache/nginx への mkdir が Permission denied で exit 1 になる — 再現検証済み）。
# さらに AppRun はルートFSを読み取り専用でマウントするため（--read-only で再現検証済み）、
# envsubst の出力先を書き込み可能な /tmp 配下へ変更し、include もそこへ向ける。
# （pid・一時ファイルは unprivileged 版が元々 /tmp を使うので変更不要）
FROM nginxinc/nginx-unprivileged:1.27-alpine

# /etc/nginx/templates/*.template は起動時に envsubst され
# NGINX_ENVSUBST_OUTPUT_DIR に展開される（テンプレート機能は unprivileged 版でも同じ）
COPY default.conf.template /etc/nginx/templates/default.conf.template

ENV NGINX_ENVSUBST_OUTPUT_DIR=/tmp/conf.d

USER root
RUN sed -i 's|include /etc/nginx/conf.d/\*.conf;|include /tmp/conf.d/*.conf;|' /etc/nginx/nginx.conf \
 && printf '#!/bin/sh\nmkdir -p /tmp/conf.d\n' > /docker-entrypoint.d/05-mkdir-output-dir.sh \
 && chmod +x /docker-entrypoint.d/05-mkdir-output-dir.sh
USER nginx

# ポートは 8081（AppRun はホストNW共有のため api の 8080 と同居できない）
EXPOSE 8081
