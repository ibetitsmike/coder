#!/usr/bin/env bash
# Starts Postgres 16 for fork-ci on 127.0.0.1:5432 (postgres:postgres). A
# service container cannot pass server flags, and the test suites open more
# than the default 100 connections.
set -euo pipefail

docker run -d --name fork-ci-postgres -p 5432:5432 \
	-e POSTGRES_PASSWORD=postgres \
	--tmpfs /var/lib/postgresql/data \
	postgres:16 \
	-c max_connections=1000 \
	-c fsync=off \
	-c synchronous_commit=off \
	-c full_page_writes=off

# The image runs a socket-only server while it initializes, so wait for TCP.
for _ in $(seq 60); do
	if docker exec fork-ci-postgres pg_isready -h 127.0.0.1 -U postgres >/dev/null; then
		exit 0
	fi
	sleep 1
done
docker logs fork-ci-postgres
exit 1
