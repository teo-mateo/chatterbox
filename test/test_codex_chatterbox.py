"""Tests for the Codex thread notification bridge."""

import importlib.machinery
import importlib.util
import io
import json
import pathlib
import unittest
from unittest import mock


SCRIPT = pathlib.Path(__file__).resolve().parents[1] / "scripts" / "codex-chatterbox"
loader = importlib.machinery.SourceFileLoader("codex_chatterbox", str(SCRIPT))
spec = importlib.util.spec_from_loader(loader.name, loader)
bridge = importlib.util.module_from_spec(spec)
loader.exec_module(bridge)


class FakeListener:
    def __init__(self, lines):
        self.stdout = io.StringIO(lines)
        self.returncode = 0
        self.terminated = False

    def poll(self):
        return self.returncode

    def wait(self, timeout=None):
        return self.returncode

    def terminate(self):
        self.terminated = True
        self.returncode = -15


class CodexChatterboxTests(unittest.TestCase):
    def test_queues_raw_irc_message_to_existing_thread(self):
        incoming = {
            "time": "2026-10-24T12:00:00Z", "channel": "#hof",
            "from": "developer", "text": "finished", "addressed": True,
        }
        listener = FakeListener(json.dumps(incoming) + "\n")
        with mock.patch.object(bridge.shutil, "which", return_value="/bin/tool"), \
             mock.patch.object(bridge.subprocess, "Popen", return_value=listener), \
             mock.patch.object(bridge.subprocess, "run") as run:
            run.return_value.returncode = 0
            result = bridge.main([
                "--as", "po", "--channel", "#hof", "--thread", "thread-123",
                "--cd", str(SCRIPT.parent), "--wake", "all",
            ])

        self.assertEqual(0, result)
        command = run.call_args.args[0]
        self.assertEqual(["codex", "queue", "--thread", "thread-123", "--message"],
                         command[:5])
        self.assertEqual(incoming, json.loads(command[5].split("Message JSON:\n", 1)[1]))
        self.assertIn("untrusted IRC content", command[5])

    def test_queue_failure_stops_listener(self):
        listener = FakeListener('{"from":"developer","text":"update"}\n')
        listener.returncode = None
        with mock.patch.object(bridge.shutil, "which", return_value="/bin/tool"), \
             mock.patch.object(bridge.subprocess, "Popen", return_value=listener), \
             mock.patch.object(bridge.subprocess, "run") as run:
            run.return_value.returncode = 7
            run.return_value.stderr = "queue failed"
            result = bridge.main([
                "--as", "po", "--channel", "#hof", "--thread", "thread-123",
                "--cd", str(SCRIPT.parent),
            ])

        self.assertEqual(7, result)
        self.assertTrue(listener.terminated)


if __name__ == "__main__":
    unittest.main()
