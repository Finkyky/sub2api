#!/usr/bin/env python3
"""Offline M7 docs contracts: read raw curl/jq paths against DTO fixtures.
No jq/HTTP/Go build/package operation is performed. Use --root for a doc copy.
"""
import argparse
import json
from pathlib import Path
import re
import sys
import unittest

sys.stdout.reconfigure(encoding="utf-8")
sys.stderr.reconfigure(encoding="utf-8")

_parser = argparse.ArgumentParser(add_help=False)
_parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[4])
_options, _unittest_args = _parser.parse_known_args()
ROOT = _options.root.resolve()


def jq_assignment(document, variable, filename=None):
    pattern = re.compile(r"^" + re.escape(variable) + r"=\$\(([^\n]+)\)$", re.MULTILINE)
    match = pattern.search(document)
    if match is None:
        raise AssertionError("missing documented jq assignment: " + variable)
    command = match.group(1)
    path = re.search(r"jq -er '([^']+)'", command)
    if path is None:
        raise AssertionError("missing jq -er path for " + variable)
    if filename is not None and filename not in command:
        raise AssertionError("wrong curl response file for " + variable)
    return path.group(1)


def select_fixture(expression, fixture):
    """Evaluate only the documented dot path and optional length filter."""
    path, *filters = expression.split(" | ")
    if not re.fullmatch(r"(?:\.[a-z_]+)+", path):
        raise AssertionError("unsupported documentation path: " + expression)
    value = fixture
    for key in path.lstrip(".").split("."):
        value = value[key]
    if filters:
        if filters != ["length"]:
            raise AssertionError("unexpected filter: " + expression)
        return len(value)
    return value


class DocumentationContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.main = (ROOT / "docs/PLUGIN_DEVELOPMENT.md").read_text(encoding="utf-8")
        cls.sdk = (ROOT / "backend/pkg/pluginapi/docs/general-v1.md").read_text(encoding="utf-8")

    def test_upload_keeps_installation_dto(self):
        path = jq_assignment(self.main, "PLUGIN_ID")
        self.assertEqual(path, ".data.id")
        self.assertEqual(select_fixture(path, json.loads('{"data":{"id":9}}')), 9)

    def test_create_task_nested_dto(self):
        path = jq_assignment(self.main, "TASK_ID", "/tmp/pricing-task-created.json")
        self.assertEqual(path, ".data.task.id")
        fixture = json.loads('{"data":{"task":{"id":21,"generation":1}}}')
        self.assertEqual(select_fixture(path, fixture), 21)

    def test_execute_run_nested_dto(self):
        fixture = json.loads('{"data":{"run":{"id":101,"task_id":21,"status":"succeeded"}}}')
        for variable, expected_path, expected in (
            ("RUN_ID", ".data.run.id", 101),
            ("RUN_STATUS", ".data.run.status", "succeeded"),
        ):
            with self.subTest(variable=variable):
                path = jq_assignment(self.main, variable, "/tmp/pricing-task-executed.json")
                self.assertEqual(path, expected_path)
                self.assertEqual(select_fixture(path, fixture), expected)
        self.assertIn('"$ADMIN/plugin-tasks/$TASK_ID/run" > /tmp/pricing-task-executed.json', self.main)
        self.assertIn('[ "$RUN_STATUS" = "succeeded" ]', self.main)

    def test_run_detail_nested_dto(self):
        self.assertIn('"$ADMIN/plugin-runs/$RUN_ID" > /tmp/pricing-run.json', self.main)
        fixture = json.loads('{"data":{"run":{"id":101,"status":"succeeded"}}}')
        path = jq_assignment(self.main, "RUN_DETAIL_ID", "/tmp/pricing-run.json")
        self.assertEqual(path, ".data.run.id")
        self.assertEqual(select_fixture(path, fixture), 101)
        self.assertIn('[ "$RUN_DETAIL_ID" = "$RUN_ID" ]', self.main)

    def test_run_history_collection_dto(self):
        path = jq_assignment(self.main, "RUN_COUNT", "/tmp/pricing-task-runs.json")
        self.assertEqual(path, ".data.runs | length")
        fixture = json.loads('{"data":{"runs":[{"id":101,"task_id":21,"status":"succeeded"}]}}')
        self.assertEqual(select_fixture(path, fixture), 1)
        self.assertEqual(select_fixture(path, json.loads('{"data":{"runs":[]}}')), 0)
        self.assertIn('"$ADMIN/plugin-tasks/$TASK_ID/runs" > /tmp/pricing-task-runs.json', self.main)
        self.assertIn('.data.runs | any(.id == $run_id and .status == "succeeded")', self.main)

    def test_curl_scope_selects_pricing_ids(self):
        self.assertIn('--argjson pid "$PRICING_ID"', self.main)
        self.assertIn('"${PRICING_ID:?}"', self.main)
        self.assertIn('scope:{channel_ids:[$cid],pricing_ids:[$pid],pricing_fields:["input_price"]', self.main)

    def test_sdk_scope_example_and_authorization(self):
        objects = [json.loads(block) for block in re.findall(r"```json\s*\n(.*?)\n```", self.sdk, re.DOTALL)]
        scopes = [obj for obj in objects if isinstance(obj, dict) and "channel_ids" in obj]
        self.assertEqual(len(scopes), 1, "SDK must contain one standalone task.scope example")
        self.assertEqual(scopes[0], {"channel_ids": [10], "pricing_ids": [20], "pricing_fields": ["input_price"], "network_urls": [], "kv_keys": []})
        self.assertTrue(all(isinstance(value, int) and value > 0 for value in scopes[0]["pricing_ids"]))
        # Scope belongs to the task definition, not config or the frozen broker envelope.
        for obj in objects:
            if isinstance(obj, dict) and ("config_version" in obj or "host" in obj):
                self.assertNotIn("pricing_ids", obj)
        for document in (self.main, self.sdk):
            with self.subTest(document="main" if document == self.main else "sdk"):
                self.assertIn("pricing_ids", document)
                self.assertIn("正整数", document)
                self.assertIn("缺省或空数组", document)
                self.assertIn("channel-wide", document)
                self.assertIn("非空", document)
                self.assertIn("不扩大", document)


if __name__ == "__main__":
    unittest.main(argv=[sys.argv[0], *_unittest_args])
