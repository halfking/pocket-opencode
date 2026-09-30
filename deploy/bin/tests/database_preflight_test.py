import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('preflight', Path(__file__).parents[1] / 'lib/check-databases.py')
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)


class PreflightTest(unittest.TestCase):
    def setUp(self):
        self.values = {'POCKET_POSTGRES_DSN': 'postgresql://user:p%40ss@host.docker.internal:5432/pocket?sslmode=disable',
                       'POCKET_PG_SCHEMA': 'opencode_pocket'}

    def pg(self, reply):
        with patch.object(mod, 'client', return_value='psql'), patch.object(mod, 'run', return_value=reply) as call, contextlib.redirect_stdout(io.StringIO()):
            mod.check_pg(self.values)
        return call

    def test_existing_schema_auth_and_privileges(self):
        call = self.pg('t|t|t|t|f|0|0')
        args, kwargs = call.call_args
        self.assertNotIn('p@ss', str(args))
        self.assertNotIn(self.values['POCKET_POSTGRES_DSN'], str(args))
        self.assertEqual(kwargs['env']['PGPASSWORD'], 'p@ss')
        self.assertEqual(kwargs['env']['PGHOST'], '127.0.0.1')
        self.assertIn('default_transaction_read_only=on', kwargs['env']['PGOPTIONS'])

    def test_migration_can_create_schema(self):
        self.pg('t|f|f|f|t|0|0')

    def test_missing_schema_without_create_denied(self):
        with self.assertRaisesRegex(mod.CheckError, 'CREATE privilege'):
            self.pg('t|f|f|f|f|0|0')

    def test_schema_usage_or_create_missing_denied(self):
        for reply in ('t|t|f|t|t|0|0', 't|t|t|f|t|0|0'):
            with self.subTest(reply=reply), self.assertRaises(mod.CheckError):
                self.pg(reply)

    def test_table_or_sequence_privileges_missing(self):
        for reply in ('t|t|t|t|t|1|0', 't|t|t|t|t|0|1'):
            with self.subTest(reply=reply), self.assertRaises(mod.CheckError):
                self.pg(reply)

    def test_database_connect_denied(self):
        with self.assertRaises(mod.CheckError):
            self.pg('f|t|t|t|t|0|0')

    def test_schema_sql_injection_rejected(self):
        self.values['POCKET_PG_SCHEMA'] = "x'; DROP SCHEMA public; --"
        with self.assertRaisesRegex(mod.CheckError, 'valid POCKET_PG_SCHEMA'):
            self.pg('t|t|t|t|t|0|0')

    def test_missing_dsn_rejected(self):
        self.values.clear()
        with self.assertRaises(mod.CheckError):
            self.pg('t|t|t|t|t|0|0')

    def test_unsupported_url_option_rejected(self):
        self.values['POCKET_POSTGRES_DSN'] += '&unknown=secret'
        with self.assertRaisesRegex(mod.CheckError, 'unsupported'):
            self.pg('t|t|t|t|t|0|0')

    def test_redis_noauth_even_success_exit_rejected(self):
        with patch.object(mod, 'client', return_value='redis-cli'), patch.object(mod, 'run', return_value='NOAUTH Authentication required.'):
            with self.assertRaises(mod.CheckError):
                mod.check_redis({'POCKET_REDIS_URL': 'redis://:pw@localhost/1'})

    def test_redis_password_not_in_args(self):
        with patch.object(mod, 'client', return_value='redis-cli'), patch.object(mod, 'run', return_value='PONG') as call, contextlib.redirect_stdout(io.StringIO()):
            mod.check_redis({'POCKET_REDIS_URL': 'redis://user:p%40ss@localhost/1'})
        self.assertNotIn('p@ss', str(call.call_args.args))
        self.assertEqual(call.call_args.kwargs['env']['REDISCLI_AUTH'], 'p@ss')

    def inventory(self, items, occupied=False):
        def connect(*args, **kwargs):
            if occupied:
                return contextlib.nullcontext()
            raise OSError('unreachable')
        with patch.object(mod.socket, 'create_connection', side_effect=connect), patch.object(mod, 'client', return_value='docker'), patch.object(mod, 'run', side_effect=['id' if items else '', json.dumps(items)]), contextlib.redirect_stdout(io.StringIO()):
            mod.can_create('postgres', '127.0.0.1', 15432)

    def test_unrelated_container_name_does_not_block(self):
        self.inventory([{'Id': '123456789abc', 'Name': 'postgres', 'Config': {'Image': 'nginx:alpine'}}])

    def test_unknown_image_with_pg_port_blocks(self):
        with self.assertRaisesRegex(mod.CheckError, 'candidate'):
            self.inventory([{'Id': '123456789abc', 'Config': {'Image': 'sha256:abc', 'ExposedPorts': {'5432/tcp': {}}}}])

    def test_citus_image_without_published_port_blocks(self):
        with self.assertRaisesRegex(mod.CheckError, 'candidate'):
            self.inventory([{'Id': '123456789abc', 'Config': {'Image': 'citusdata/citus:13'}}])

    def test_occupied_target_port_blocks(self):
        with self.assertRaisesRegex(mod.CheckError, 'occupied'):
            self.inventory([], occupied=True)

    def test_no_candidates_permits_creation(self):
        self.inventory([])

    def test_remote_target_cannot_create_locally(self):
        with self.assertRaisesRegex(mod.CheckError, 'local target'):
            mod.can_create('postgres', 'db.example', 5432)

    def test_client_failure_redacted(self):
        result = subprocess.CompletedProcess(['psql'], 1, '', 'password=should-never-appear')
        with patch.object(mod.subprocess, 'run', return_value=result), self.assertRaises(mod.CheckError) as ctx:
            mod.run(['psql'])
        self.assertNotIn('should-never-appear', str(ctx.exception))

    def test_dotenv_quotes_no_code_execution(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / '.env'
            path.write_text("# ignored\nA='value'\nB=$(false)\n")
            self.assertEqual(mod.read_env(path), {'A': 'value', 'B': '$(false)'})


if __name__ == '__main__':
    result = unittest.TextTestRunner(verbosity=2).run(unittest.defaultTestLoader.loadTestsFromTestCase(PreflightTest))
    failures = len(result.failures) + len(result.errors)
    print(f"  PASS: {result.testsRun - failures}  FAIL: {failures}")
    raise SystemExit(0 if result.wasSuccessful() else 1)
