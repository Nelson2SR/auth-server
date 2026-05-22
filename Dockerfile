# Casdoor image for Render, with our config + provisioning baked in.
# Render builds this from the repo; the resulting Web Service needs no volumes.
#
# conf/app.conf reads CASDOOR_DSN (assembled by the entrypoint from the Render
# database's host/user/password/etc.) and RENDER_EXTERNAL_URL (the public URL
# Render injects) for the OIDC issuer/origin. Both fall back to local-dev
# defaults when unset, so this image also works under docker-compose.
FROM casbin/casdoor:v1.812.0

# WorkingDir is "/"; Casdoor reads conf/app.conf and init_data.json from there.
COPY conf/app.conf /conf/app.conf
COPY init_data.json /init_data.json
# No-op init file. Casdoor re-applies initDataFile on EVERY boot and overwrites
# existing rows, which would revert runtime-injected provider secrets back to the
# REPLACE_* placeholders. On Render we point INIT_DATA_FILE at this empty file so
# the first boot seeds from init_data.json and later boots import nothing.
COPY deploy/noop_init.json /noop_init.json
COPY deploy/render-entrypoint.sh /render-entrypoint.sh

ENTRYPOINT ["/render-entrypoint.sh"]
