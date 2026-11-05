FROM ghost:6.75.0
# Ghost keeps images, themes and its log files in its content folder, here
# /data: paths__contentPath tells Ghost, GHOST_CONTENT tells the image's start
# script, which copies the default theme there on the first start. The image's
# own /var/lib/ghost/content volume is left unused. Started as node, the script
# skips its chown and its switch from root to node.
ENV GHOST_CONTENT=/data \
    paths__contentPath=/data \
    server__host=0.0.0.0 \
    server__port=2368 \
    database__client=mysql
USER node
EXPOSE 2368
# The first start creates the database tables, which can take minutes on half a
# CPU. Ghost redirects plain-HTTP requests to its https address; any answer
# below 500 means it is up.
HEALTHCHECK --interval=30s --timeout=5s --start-period=3m --start-interval=2s --retries=3 \
  CMD ["node", "-e", "fetch('http://127.0.0.1:2368/ghost/api/admin/site/', {redirect: 'manual'}).then(r => process.exit(r.status < 500 ? 0 : 1), () => process.exit(1))"]
