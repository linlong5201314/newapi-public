FROM nginx:1.27-alpine
COPY nginx.conf /etc/nginx/nginx.conf
RUN mkdir -p /var/cache/nginx/static && nginx -t
EXPOSE 8080
