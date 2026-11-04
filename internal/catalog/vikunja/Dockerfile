FROM vikunja/vikunja:2.7.1
# Vikunja requires the files folder to exist already, so attachments go
# straight into /data next to the database.
ENV VIKUNJA_SERVICE_INTERFACE=0.0.0.0:3456 \
    VIKUNJA_DATABASE_TYPE=sqlite \
    VIKUNJA_DATABASE_PATH=/data/vikunja.db \
    VIKUNJA_FILES_BASEPATH=/data
USER 1000
EXPOSE 3456
