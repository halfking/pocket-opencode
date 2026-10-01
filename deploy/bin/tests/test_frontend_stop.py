"""Scoped stop protocol tests; fake Docker is not deployment acceptance."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[3]


class FrontendStop(unittest.TestCase):
    def run_stop(self, flags=(), expected="a" * 64, actual="a" * 64, target="local"):
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            (base / "config").mkdir()
            (base / "config/.env.local").write_text("")
            binary = base / "fake-bin"
            binary.mkdir()
            docker = binary / "docker"
            docker.write_text('#!/bin/bash\nprintf "%s\\n" "$*" >> "$CALLS"\n'
                              'if [[ " $* " == *" ps --all -q frontend "* ]]; then echo "$ACTUAL"; fi\n'
                              'if [[ "$1" == "inspect" ]]; then echo "$LABELS"; fi\n')
            docker.chmod(0o700)
            env = {**os.environ, "PATH": str(binary) + ":" + os.environ["PATH"],
                   "DEPLOY_BASE_DIR": tmp, "DEPLOY_ENV": target, "CALLS": str(base / "calls"),
                   "ACTUAL": actual, "OPP_EXPECT_FRONTEND_ID": expected,
                   "LABELS": "opencode-pocket-local-" + base.name + "|frontend"}
            run = subprocess.run(["bash", str(ROOT / "deploy-local.sh"), "--stop-frontend", *flags],
                                 env=env, capture_output=True, text=True, timeout=10)
            calls = (base / "calls").read_text() if (base / "calls").exists() else ""
            return run.returncode, calls

    def test_only_frontend_stops(self):
        code, calls = self.run_stop()
        self.assertEqual(code, 0)
        self.assertIn("stop --time 10 " + "a" * 64, calls)
        self.assertNotIn("stop frontend", calls)
        self.assertNotIn("down", calls)
        self.assertNotIn("pocketd", calls)

    def test_drift_refused(self):
        code, calls = self.run_stop(actual="b" * 64)
        self.assertNotEqual(code, 0)
        self.assertNotIn("stop --time", calls)

    def test_missing_id_refused(self):
        code, calls = self.run_stop(expected="")
        self.assertNotEqual(code, 0)
        self.assertEqual(calls, "")

    def test_extra_flags_refused_before_discovery(self):
        code, calls = self.run_stop(flags=("--volumes",))
        self.assertNotEqual(code, 0)
        self.assertEqual(calls, "")

    def test_remote_target_refused(self):
        code, calls = self.run_stop(target="server")
        self.assertNotEqual(code, 0)
        self.assertEqual(calls, "")


if __name__ == "__main__":
    unittest.main()
