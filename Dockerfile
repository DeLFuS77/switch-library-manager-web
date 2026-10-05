FROM alpine:3

RUN apk add --no-cache tzdata su-exec

ENV SLM_DATA_DIR=/usr/local/share/switch-library-manager-web \
    PUID=1000 \
    PGID=1000

RUN mkdir -p $SLM_DATA_DIR /mnt/roms

COPY build/switch-library-manager-web /usr/local/bin/switch-library-manager-web
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod 755 /usr/local/bin/switch-library-manager-web /usr/local/bin/docker-entrypoint.sh

VOLUME $SLM_DATA_DIR
VOLUME /mnt/roms

EXPOSE 3000

# /healthz answers without authentication; the port must match "port" in settings.json
HEALTHCHECK --interval=30s --timeout=5s --start-period=2m --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:3000/healthz || exit 1

ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["switch-library-manager-web"]
