#!/bin/sh
# Render entrypoint for Casdoor.
#
# Beego's app.conf only expands a SINGLE whole-value ${VAR||default}, so we
# cannot compose the Postgres DSN inline from separate fields. Instead we
# assemble the keyword-format DSN here from Render's individual database
# properties and export it as CASDOOR_DSN, which app.conf reads as one value.
#
# When DB_HOST is unset (e.g. local docker-compose), CASDOOR_DSN stays unset and
# app.conf falls back to its built-in default DSN.
set -e

if [ -n "$DB_HOST" ]; then
  export CASDOOR_DSN="user=${DB_USER} password=${DB_PASSWORD} host=${DB_HOST} port=${DB_PORT:-5432} sslmode=${DB_SSLMODE:-require} dbname=${DB_NAME}"
fi

exec /server
