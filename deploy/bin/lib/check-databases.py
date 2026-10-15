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


DB_ENGINE_SIGNATURE = {"postgres": r"postgres|pgvector|citus",
                       "redis": r"redis|valkey|keydb",
                       "mysql": r"mysql|mariadb|percona"}
DB_INTERNAL_PORT = {"postgres": 5432, "redis": 6379, "mysql": 3306}


def engine_container(kind, port):
    """Name of a running container that both runs `kind` and publishes `port`.

    Only a container that actually publishes the configured host port may stand
    in for the target instance. A merely similar container could answer on a
    different endpoint and make reuse look verified when nothing was verified.
    """
    if not shutil.which("docker"):
        return None
    try:
        ids = run([client("docker"), "ps", "-q"]).split()
        if not ids:
            return None
        items = json.loads(run([client("docker"), "inspect", *ids]))
    except (CheckError, OSError, ValueError):
        return None
    signature = re.compile(DB_ENGINE_SIGNATURE[kind], re.I)
    for item in items:
        bindings = item.get("NetworkSettings", {}).get("Ports") or {}
        published = any(any(str(b.get("HostPort")) == str(port) for b in (v or []) if b)
                        for v in bindings.values())
        if not published:
            continue
        name = (item.get("Name") or "").lstrip("/")
        image = item.get("Config", {}).get("Image", "")
        if signature.search(f"{image} {name}"):
            return name
    return None


def db_client(kind, binary, host, port, forward_env=()):
    """Command prefix for a database client, pointed at the right endpoint.

    Prefers a host binary and talks to `host:port` directly. On Docker-only dev
    hosts — no libpq, no redis-cli, no mysqladmin — borrow the client from the
    container that publishes `port`, and talk to that container's own loopback
    port instead of the host's published one.

    Secret environment variables are forwarded as bare `-e NAME`, which Docker
    reads from this process without ever placing the value in argv. That keeps
    the "credentials must not appear in the process table" property intact.
    """
    # Host and port are not secret, so they travel as plain flags either way.
    port_flag = "-P" if binary.startswith("mysql") else "-p"
    found = client_or_none(binary)
    if found:
        return [found, "-h", host, port_flag, str(port)]
    container = engine_container(kind, port)
    if not container:
        raise CheckError(f"{binary} client unavailable and no {kind} container publishes {port}")
    prefix = ["docker", "exec", "-i"]
    for name in forward_env:
        prefix += ["-e", name]
    internal = DB_INTERNAL_PORT[kind]
    return prefix + [container, binary, "-h", "127.0.0.1", port_flag, str(internal)]


def client_or_none(name):
    """Host path for a client binary, or None when this host has none."""
    found = shutil.which(name)
    if not found and name == "psql":
        # Homebrew libpq is intentionally keg-only.
        found = next((str(p) for p in [Path("/opt/homebrew/opt/libpq/bin/psql"),
                                      Path("/usr/local/opt/libpq/bin/psql")]
                      if p.is_file()), None)
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


def read_compose_env(env_file, compose_file):
    # Use the same Compose resolver as rollout: interpolation, quoting, comments
    # and service environment overrides must be identical to the runtime.
    rendered = json.loads(run([client("docker"), "compose", "--env-file", env_file,
                               "-f", compose_file, "config", "--format", "json"]))
    values = rendered.get("services", {}).get("pocketd", {}).get("environment")
    if not isinstance(values, dict):
        raise CheckError("rendered pocketd environment unavailable")
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
    args = db_client("postgres", "psql", host, port,
                     forward_env=("PGUSER", "PGPASSWORD", "PGDATABASE",
                                  "PGCONNECT_TIMEOUT", "PGOPTIONS")) \
        + ["-X", "-w", "-tA", "-v", "ON_ERROR_STOP=1", "-v", f"schema={schema}"]
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
    args = db_client("redis", "redis-cli", host, port, forward_env=("REDISCLI_AUTH",))
    if parsed.username:
        args += ["--user", unquote(parsed.username)]
    if parsed.scheme == "rediss":
        args += ["--tls"]
    if parsed.path.strip("/"):
        args += ["-n", parsed.path.strip("/")]
    if run(args + ["PING"], env=env) != "PONG":
        raise CheckError("Redis PING denied or unexpected protocol")
    print(f"Redis {host}:{port}: authenticated PONG")


def inventory(kind):
    # Native daemons can listen on non-default ports. A process candidate blocks
    # creation until its endpoint is configured and verified; it is not a reuse
    # success. ps comm excludes command arguments/credentials.
    names = {"postgres": {"postgres", "postmaster"},
             "redis": {"redis-server", "valkey-server", "keydb-server"},
             "mysql": {"mysqld", "mariadbd"}}
    candidates = []
    for line in run([client("ps"), "-axo", "pid=,comm="]).splitlines():
        fields = line.strip().split(None, 1)
        if len(fields) == 2 and fields[0].isdigit():
            executable = Path(fields[1].split()[0]).name.rstrip(":")
            if executable in names[kind]:
                candidates.append({"source": "native-process", "id": fields[0]})
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
                labels = item.get("Config", {}).get("Labels") or {}
                candidates.append({"source": "docker", "id": item["Id"][:12],
                                   "compose_project": labels.get("com.docker.compose.project"),
                                   "compose_service": labels.get("com.docker.compose.service"),
                                   "networks": sorted(item.get("NetworkSettings", {}).get("Networks", {}))})
    return candidates


def can_create(kind, host, port):
    if host not in ("localhost", "127.0.0.1", "host.docker.internal"):
        raise CheckError("local database creation requires a local target")
    for candidate in (port, {"postgres": 5432, "redis": 6379, "mysql": 3306}[kind]):
        try:
            with socket.create_connection(("127.0.0.1", candidate), timeout=2):
                raise CheckError(f"local port {candidate} occupied; resolve existing resource before creation")
        except OSError:
            pass
    candidates = inventory(kind)
    if candidates:
        raise CheckError(f"running {kind} candidate {candidates[0]['source']}:{candidates[0]['id']} found; configure and verify reuse")
    print(f"{kind}: no native daemon, running Docker candidate or occupied local target/default port; creation allowed")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--env-file")
    parser.add_argument("--inventory", action="store_true")
    parser.add_argument("--compose-file")
    parser.add_argument("--can-create", choices=("postgres", "redis", "mysql"))
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int)
    args = parser.parse_args()
    try:
        if args.inventory:
            for kind in ("postgres", "redis", "mysql"):
                print(json.dumps({"kind": kind, "candidates": inventory(kind)}, ensure_ascii=False))
        elif args.can_create:
            if args.port is None or not 1 <= args.port <= 65535:
                raise CheckError("valid --port required for creation check")
            can_create(args.can_create, args.host, args.port)
        elif args.env_file:
            values = (read_compose_env(args.env_file, args.compose_file)
                      if args.compose_file else read_env(args.env_file))
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
