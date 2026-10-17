#!/bin/sh
# Runs a throwaway Postgres for development and tests in ./.pgdata on port
# 54329, so it never touches a Postgres you already have.
#
#   scripts/devdb.sh start | stop | url
set -eu
DIR="${WARRANT_PGDATA:-$(dirname "$0")/../.pgdata}"
PORT="${WARRANT_PGPORT:-54329}"
case "${1:-start}" in
start)
	if [ ! -f "$DIR/PG_VERSION" ]; then
		initdb -D "$DIR" -U warrant --auth=trust >/dev/null
	fi
	pg_ctl -D "$DIR" -o "-p $PORT -c fsync=on" -l "$DIR/log" -w status >/dev/null 2>&1 ||
		pg_ctl -D "$DIR" -o "-p $PORT" -l "$DIR/log" -w start >/dev/null
	createdb -h localhost -p "$PORT" -U warrant warrant 2>/dev/null || true
	echo "postgres://warrant@localhost:$PORT/warrant?sslmode=disable"
	;;
stop) pg_ctl -D "$DIR" -w stop ;;
url) echo "postgres://warrant@localhost:$PORT/warrant?sslmode=disable" ;;
*) echo "usage: $0 start|stop|url" >&2; exit 2 ;;
esac
