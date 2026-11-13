#!/usr/bin/env bash
# Starts Postgres 16 for fork-ci on 127.0.0.1:5432 (postgres:postgres). A
# service container cannot pass server flags, and the test suites open more
# than the default 100 connections.
set -euo pipefail

# The image is pinned by digest: the chatdmig schema tests compare pg_dump
# output, and a republished postgres:16 tag (2026-11-12) changed that output
# (pg_attver column options) without any schema change.
docker run -d --name fork-ci-postgres -p 5432:5432 \
	-e POSTGRES_PASSWORD=postgres \
	--tmpfs /var/lib/postgresql/data \
	postgres:16@sha256:f9a7b9ebf6f56201f949db745e47bc423c0f0f8294bafe991cc10bf74b029657 \
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
