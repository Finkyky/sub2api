#!/usr/bin/env python3
"""Deployment contracts. Install PyYAML only for this test, not for start.sh."""
import argparse
from pathlib import Path
import re
import sys
import yaml

COMPOSES = ("docker-compose.yml", "docker-compose.local.yml", "docker-compose.standalone.yml", "docker-compose.dev.yml")
REQUIRED = {
    "GATEWAY_OPENAI_CODEX_CLIENT_PROFILE": "",
    "GATEWAY_DISABLE_CODEX_IDENTITY_ENFORCEMENT": "false",
    "GATEWAY_DISABLE_CODEX_ORIGINATOR_NORMALIZATION": "false",
    "GATEWAY_CODEX_IMAGE_GENERATION_BRIDGE_ENABLED": "false",
    "GATEWAY_CONNECTION_POOL_ISOLATION": "account_proxy",
    "GATEWAY_TLS_FINGERPRINT_ENABLED": "true",
    "GATEWAY_OPENAI_WS_ENABLED": "true",
    "GATEWAY_OPENAI_WS_MODE_ROUTER_V2_ENABLED": "false",
    "GATEWAY_OPENAI_WS_INGRESS_MODE_DEFAULT": "ctx_pool",
    "GATEWAY_OPENAI_CODEX_TICKET_ENABLED": "false",
    "GATEWAY_OPENAI_CODEX_TICKET_HARVEST_PROXY_URL": "",
    "GATEWAY_OPENAI_CODEX_TICKET_FAIL_CLOSED": "true",
    "GATEWAY_RESULT_CACHE_IO_TIMEOUT_MS": "300",
    "GATEWAY_RESULT_CACHE_WAIT_TIMEOUT_MS": "2000",
    "GATEWAY_RESULT_CACHE_LEASE_TTL_MS": "30000",
    "GATEWAY_RESULT_CACHE_GENERATION_TIMEOUT_MS": "600000",
    "GATEWAY_RESULT_CACHE_MAX_CONCURRENT_FILLS": "32",
    "PLUGINS_DATA_DIR": "/app/data/plugins",
    "PLUGINS_ALLOW_UNSIGNED": "false",
    "PLUGINS_START_TIMEOUT_SECONDS": "15",
    "PLUGINS_INSTANCE_RECONCILE_INTERVAL_SECONDS": "5",
    "PLUGINS_INSTANCE_RECONCILE_TIMEOUT_SECONDS": "30",
    "PLUGINS_GATEWAY_HOOK_TIMEOUT_SECONDS": "10",
    "PLUGINS_STRICT_OUTPUT_TIMEOUT_SECONDS": "120",
    "PLUGINS_STRICT_OUTPUT_MAX_BYTES": "8388608",
    "PLUGINS_STRICT_OUTPUT_TOTAL_BYTES": "134217728",
    "PLUGINS_STRICT_OUTPUT_MAX_CONCURRENCY": "16",
    "SERVER_FRONTEND_URL": "",
}


def check(root):
    deploy = root / "deploy"
    template = (deploy / ".env.example").read_text(encoding="utf-8")
    env = dict(re.findall(r"^([A-Z][A-Z0-9_]*)=(.*)$", template, re.M))
    for key, default in REQUIRED.items():
        assert env.get(key) == default, f".env.example must document {key}={default}"
    assert "SERVER_TRUSTED_PROXIES" not in env, "default must not erase persisted trusted proxies"
    assert env["SECURITY_TRUST_FORWARDED_IP_FOR_API_KEY_ACL"] == "false"
    for name in COMPOSES:
        config = yaml.safe_load((deploy / name).read_text(encoding="utf-8"))
        app = config["services"]["sub2api"]
        entries = app["environment"]
        assert len(entries) == len({entry.split("=", 1)[0] for entry in entries}), f"duplicate env in {name}"
        mapping = {entry.partition("=")[0]: entry.partition("=")[2] if "=" in entry else None for entry in entries}
        for optional in ("SERVER_TRUSTED_PROXIES", "SECURITY_TRUST_FORWARDED_IP_FOR_API_KEY_ACL"):
            assert entries.count(optional) == 1, f"{name}: preserve absent optional {optional}"
        for key, default in REQUIRED.items():
            assert mapping.get(key) == "${" + key + ":-" + default + "}", f"{name}: missing mapping {key}"
        if name != "docker-compose.dev.yml":
            assert app["image"] == "${SUB2API_IMAGE:-weishaw/sub2api:latest}", f"{name}: image must be configurable"
        assert "no-new-privileges:true" in app["security_opt"]

    source = yaml.safe_load((deploy / "docker-compose.source.yml").read_text(encoding="utf-8"))["services"]["sub2api"]
    assert source["build"]["context"] == ".."
    assert source["build"]["dockerfile"] == "Dockerfile"
    assert source["image"] == "${SUB2API_SOURCE_IMAGE:-sub2api-custom:local}"
    assert source["pull_policy"] == "never"
    assert "SERVER_MODE=debug" not in source.get("environment", [])
    https = yaml.safe_load((deploy / "docker-compose.https.yml").read_text(encoding="utf-8"))
    caddy = https["services"]["caddy"]
    assert "80:80" in caddy["ports"] and "443:443" in caddy["ports"]
    assert "sub2api-network" in caddy["networks"]
    assert caddy["networks"]["sub2api-network"]["ipv4_address"] == "${SUB2API_HTTPS_PROXY_IP:-172.30.50.10}"
    assert "SERVER_TRUSTED_PROXIES=${SUB2API_HTTPS_PROXY_IP:-172.30.50.10}/32" in https["services"]["sub2api"]["environment"]
    assert "SECURITY_TRUST_FORWARDED_IP_FOR_API_KEY_ACL=false" in https["services"]["sub2api"]["environment"]
    assert "./Caddyfile.docker:/etc/caddy/Caddyfile:ro" in caddy["volumes"]
    assert "caddy_data" in https["volumes"]
    caddyfile = (deploy / "Caddyfile.docker").read_text(encoding="utf-8")
    for expected in ("{$SITE_DOMAIN}", "reverse_proxy sub2api:8080", "flush_interval -1", "header_up X-Forwarded-For {remote_host}"):
        assert expected in caddyfile, f"Caddyfile missing {expected}"
    assert "encode" not in caddyfile, "baseline must not compress streaming responses"
    config = yaml.safe_load((deploy / "config.example.yaml").read_text(encoding="utf-8"))
    assert config["gateway"]["openai_codex_client_profile"] == ""
    assert config["gateway"]["disable_codex_identity_enforcement"] is False
    assert config["gateway"]["disable_codex_originator_normalization"] is False
    assert config["plugins"]["allow_unsigned"] is False
    docs = (deploy / "ONE_CLICK_CN.md").read_text(encoding="utf-8")
    for expected in ("--codex-aligned", "--https", "--init-only", "--image", "SMTP", "codex_fingerprint_mode", "迁移", "回滚"):
        assert expected in docs, f"guide missing {expected}"
    assert "deploy/start.sh" in (root / "docs/CODEX_LINUX_ALIGNMENT.md").read_text(encoding="utf-8"), "alignment docs must link startup script"
    print(f"one-click configuration contracts passed ({len(COMPOSES)} compose variants, {len(REQUIRED) + 2} settings)")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[2])
    args = parser.parse_args()
    try:
        check(args.root)
    except (AssertionError, FileNotFoundError, KeyError) as exc:
        print(f"FAIL: {exc}", file=sys.stderr)
        sys.exit(1)
