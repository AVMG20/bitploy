FROM filebrowser/filebrowser:v2.63.23
# The files are in /data/files and the database in /data/filebrowser.db, so
# the database is not among the files people browse. The image keeps both in
# separate volumes (/srv, /database); the first start creates the folder.
# At the first start File Browser makes the user admin with a random password
# and prints it in the log.
ENV FB_ADDRESS=0.0.0.0 \
    FB_PORT=8080 \
    FB_ROOT=/data/files \
    FB_DATABASE=/data/filebrowser.db
USER 1000:1000
EXPOSE 8080
ENTRYPOINT ["tini", "--", "sh", "-c", "mkdir -p /data/files && exec /init.sh"]
