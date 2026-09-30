#!/usr/bin/env python3
"""Read-only database preflight. Never print DSNs, credentials or client errors."""
import argparse
import json
import os
from pathlib import Path
import re
import shutil
import socket
import subprocess
import sys
from urllib.parse import unquote, urlsplit


class CheckError(Exception):
    pass


def run(args, *, env=None, input=None):
    try:
        result = subprocess.run(args, env=env, input=input, text=True,
                                capture_output=True, timeout=12)
    except (OSError, subprocess.TimeoutExpired):
        raise CheckError("client unavailable or timed out") from None
    if result.returncode:
        raise CheckError("connection, authentication or query failed")
    return result.stdout.strip()


def client(name):
    found = shutil.which(name)
    if not found and name == "psql":
        # Homebrew libpq is intentionally keg-only.
        found = next((str(p) for p in [Path("/opt/homebrew/opt/libpq/bin/psql"),
                                      Path("/usr/local/opt/libpq/bin/psql")]
                      if p.is_file()), None)
    if not found:
        raise CheckError(f"{name} client required for preflight")
    return found


def read_env(path):
    values = {}
    for line in Path(path).read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("export "):
            line = line[7:]
        if "=" not in line:
            continue
        key, value = line.split("=", 1)
        value = value.strip()
        if value[:1] in ("'", '"') and value[-1:] == value[:1]:
            value = value[1:-1]
        values[key.strip()] = value
    return values


def endpoint(dsn, schemes, default_port):
    try:
        parsed = urlsplit(dsn)
        if parsed.scheme not in schemes or not parsed.hostname:
            raise ValueError()
        host = parsed.hostname
        # This alias is interpreted by Docker, and need not resolve on macOS.
        if host == "host.docker.internal":
            host = "127.0.0.1"
        return parsed, host, parsed.port or default_port
    except ValueError:
        raise CheckError("invalid database URL") from None


def check_pg(values):
    dsn = values.get("POCKET_POSTGRES_DSN") or values.get("DATABASE_URL")
    if not dsn:
        raise CheckError("POCKET_POSTGRES_DSN required; backend deployment refused")
    parsed, host, port = endpoint(dsn, ("postgres", "postgresql"), 5432)
    database = unquote(parsed.path.lstrip("/"))
    schema = values.get("POCKET_PG_SCHEMA", "opencode_pocket")
    if not database or not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", schema):
        raise CheckError("database and valid POCKET_PG_SCHEMA required")
    # PG* env avoids exposing password/DSN in process arguments. Match libpq URL
    # options, including TLS and startup options, without executing any shell.
    from urllib.parse import parse_qsl
    env = os.environ.copy()
    env.update(PGHOST=host, PGPORT=str(port), PGUSER=unquote(parsed.username or ""),
               PGPASSWORD=unquote(parsed.password or ""), PGDATABASE=database,
               PGCONNECT_TIMEOUT="5", PGOPTIONS="-c default_transaction_read_only=on")
    for key, value in parse_qsl(parsed.query):
        mapping = {"sslmode": "PGSSLMODE", "sslrootcert": "PGSSLROOTCERT",
                   "sslcert": "PGSSLCERT", "sslkey": "PGSSLKEY"}
        if key in mapping:
            env[mapping[key]] = value
        elif key == "options":
            env["PGOPTIONS"] += " " + value
        elif key != "search_path":
            raise CheckError("unsupported PostgreSQL URL option; preflight refused")
    args = [client("psql"), "-X", "-w", "-tA", "-v", "ON_ERROR_STOP=1",
            "-v", f"schema={schema}"]
    reply = run(args, env=env, input="""
SELECT has_database_privilege(current_database(), 'CONNECT'),
       EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = :'schema'),
       COALESCE((SELECT has_schema_privilege(oid, 'USAGE') FROM pg_namespace
                 WHERE nspname = :'schema'), false),
       COALESCE((SELECT has_schema_privilege(oid, 'CREATE') FROM pg_namespace
                 WHERE nspname = :'schema'), false),
       has_database_privilege(current_database(), 'CREATE'),
       (SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
        WHERE n.nspname=:'schema' AND CASE WHEN c.relkind IN ('r','p') THEN
            NOT (has_table_privilege(c.oid, 'SELECT') AND has_table_privilege(c.oid, 'INSERT')
                 AND has_table_privilege(c.oid, 'UPDATE') AND has_table_privilege(c.oid, 'DELETE')
                 AND pg_has_role(c.relowner, 'USAGE')) ELSE false END),
       (SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
        WHERE n.nspname=:'schema' AND CASE WHEN c.relkind='S' THEN NOT has_sequence_privilege(c.oid, 'USAGE') ELSE false END);

""")
    flags = reply.split("|")
    if len(flags) != 7 or flags[0] != "t":
        raise CheckError("database CONNECT privilege missing")
    if flags[1] == "t" and flags[2:4] != ["t", "t"]:
        raise CheckError("schema USAGE/CREATE privilege missing")
    if flags[1] != "t" and flags[4] != "t":
        raise CheckError("schema absent and database CREATE privilege missing")
    if flags[5:] != ["0", "0"]:
        raise CheckError("schema table write/migration ownership or sequence privilege missing")
    state = "exists, USAGE/CREATE and table/migration privileges verified" if flags[1] == "t" else "absent, migration CREATE permitted"
    print(f"PostgreSQL {host}:{port}/{database}: authenticated; schema {schema} {state}")


def check_redis(values):
    if not values.get("POCKET_REDIS_URL"):
        return
    parsed, host, port = endpoint(values["POCKET_REDIS_URL"], ("redis", "rediss"), 6379)
    env = os.environ.copy()
    env["REDISCLI_AUTH"] = unquote(parsed.password or "")
    args = [client("redis-cli"), "-h", host, "-p", str(port), "--no-auth-warning"]
    if parsed.username:
        args += ["--user", unquote(parsed.username)]
    if parsed.scheme == "rediss":
        args += ["--tls"]
    if parsed.path.strip("/"):
        args += ["-n", parsed.path.strip("/")]
    if run(args + ["PING"], env=env) != "PONG":
        raise CheckError("Redis PING denied or unexpected protocol")
    print(f"Redis {host}:{port}: authenticated PONG")


def can_create(kind, host, port):
    if host not in ("localhost", "127.0.0.1", "host.docker.internal"):
        raise CheckError("local database creation requires a local target")
    for candidate in (port, {"postgres": 5432, "redis": 6379, "mysql": 3306}[kind]):
        try:
            with socket.create_connection(("127.0.0.1", candidate), timeout=2):
                raise CheckError(f"local port {candidate} occupied; resolve existing resource before creation")
        except OSError:
            pass
    ids = run([client("docker"), "ps", "-q"]).split()
    if ids:
        containers = json.loads(run([client("docker"), "inspect", *ids]))
        signatures = {"postgres": r"postgres|pgvector|citus", "redis": r"redis|valkey|keydb",
                      "mysql": r"mysql|mariadb|percona"}
        target = {"postgres": "5432/tcp", "redis": "6379/tcp", "mysql": "3306/tcp"}[kind]
        for item in containers:
            image = item.get("Config", {}).get("Image", "")
            ports = item.get("Config", {}).get("ExposedPorts", {})
            bindings = item.get("NetworkSettings", {}).get("Ports", {})
            if re.search(signatures[kind], image, re.I) or target in ports or target in bindings:
                raise CheckError(f"running {kind} candidate {item['Id'][:12]} found; configure and verify reuse")
    print(f"{kind}: no running Docker candidate or occupied local target/default port; creation allowed")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--env-file")
    parser.add_argument("--can-create", choices=("postgres", "redis", "mysql"))
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int)
    args = parser.parse_args()
    try:
        if args.can_create:
            can_create(args.can_create, args.host, args.port)
        elif args.env_file:
            values = read_env(args.env_file)
            check_pg(values)
            check_redis(values)
        else:
            parser.error("--env-file or --can-create required")
    except (CheckError, OSError, ValueError):
        # Exceptions from client/parser libraries can contain credentials.
        error = sys.exc_info()[1]
        message = str(error) if isinstance(error, CheckError) else "configuration or inventory could not be read"
        print(f"database preflight refused: {message}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
