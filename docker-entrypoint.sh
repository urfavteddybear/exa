#!/bin/sh
set -e

# Started as root (the default): hand the data dir to the unprivileged exa user, then drop root.
# Docker creates a missing bind-mounted ./data as root:root, so this is what lets
# "git clone && docker compose up -d" work without a manual chown.
if [ "$(id -u)" = "0" ]; then
	data_dir="${DATA_DIR:-/data}"
	if ! find "$data_dir" ! -user exa -exec chown exa:exa {} + 2>/dev/null; then
		echo "exa: warning: could not chown $data_dir to exa; if startup fails, run: chown -R $(id -u exa):$(id -g exa) <host data dir>" >&2
	fi
	exec su-exec exa:exa /usr/local/bin/exa "$@"
fi

# Started with an explicit non-root user (e.g. compose "user:"): run as-is.
exec /usr/local/bin/exa "$@"
