FROM nginx:1.27-alpine

# /etc/nginx/templates/*.template は起動時に envsubst され
# /etc/nginx/conf.d/ に展開される（nginx 公式イメージの機能）
COPY default.conf.template /etc/nginx/templates/default.conf.template

EXPOSE 8080
