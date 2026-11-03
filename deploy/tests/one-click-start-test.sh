#!/usr/bin/env bash
# Offline lifecycle tests: fake Docker, real Bash/OpenSSL and temp .env files.
set -euo pipefail
REPO_ROOT=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
TEST_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/sub2api-start-test.XXXXXX")
trap 'rm -rf -- "$TEST_ROOT"' EXIT
mkdir -p "$TEST_ROOT/bin" "$TEST_ROOT/repo/deploy"
cp "$REPO_ROOT/deploy/start.sh" "$REPO_ROOT/deploy/.env.example" "$TEST_ROOT/repo/deploy/"
cp "$REPO_ROOT"/deploy/docker-compose.{local,source,https}.yml "$REPO_ROOT/deploy/docker-compose.yml" "$TEST_ROOT/repo/deploy/"
cp "$REPO_ROOT/Dockerfile" "$TEST_ROOT/repo/"
SCRIPT="$TEST_ROOT/repo/deploy/start.sh"
ENV_FILE="$TEST_ROOT/repo/deploy/.env"
export MOCK_DOCKER_LOG="$TEST_ROOT/docker.log"
export MOCK_PROFILE=codex-linux-rustls-0.153.4
export MOCK_FAIL_STAGE="" MOCK_FAIL_CODE=7 MOCK_EXISTING_STORAGE="" MOCK_EXISTING_SOURCE="" MOCK_RETAINED_VOLUME="" MOCK_EXACT_VOLUMES="" MOCK_CONTAINER_WITHOUT_MOUNT=false MOCK_INSPECT_FAIL=false
cat > "$TEST_ROOT/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >> "$MOCK_DOCKER_LOG"
printf 'selected-image=%s\n' "${SUB2API_IMAGE:-}" >> "$MOCK_DOCKER_LOG"
if [[ ${1:-} == ps ]]; then
    if [[ -n $MOCK_EXISTING_STORAGE ]] || $MOCK_CONTAINER_WITHOUT_MOUNT; then
        printf 'sub2api\nsub2api-postgres\nsub2api-redis\n'
    fi
    exit 0
fi
if [[ ${1:-} == inspect ]]; then
    if $MOCK_INSPECT_FAIL; then exit 19; fi
    if $MOCK_CONTAINER_WITHOUT_MOUNT; then exit 0; fi
fi
if [[ ${1:-} == inspect && -n $MOCK_EXISTING_STORAGE ]]; then
    case "${!#}" in
        sub2api) directory=data; volume=sub2api_data ;;
        sub2api-postgres) directory=postgres_data; volume=postgres_data ;;
        sub2api-redis) directory=redis_data; volume=redis_data ;;
    esac
    printf '%s|%s|deploy_%s\n' "$MOCK_EXISTING_STORAGE" "${MOCK_EXISTING_SOURCE:-$MOCK_DEPLOY_DIR/$directory}" "$volume"
    exit 0
fi
if [[ ${1:-} == volume && ${2:-} == ls ]]; then
    if (($# == 3)); then
        [[ -z $MOCK_EXACT_VOLUMES ]] || printf '%s\n' "$MOCK_EXACT_VOLUMES"
    else
        [[ -z $MOCK_RETAINED_VOLUME ]] || printf '%s\n' "$MOCK_RETAINED_VOLUME"
    fi
    exit 0
fi
for arg in "$@"; do
    if [[ -n $MOCK_FAIL_STAGE && $arg == "$MOCK_FAIL_STAGE" ]]; then exit "$MOCK_FAIL_CODE"; fi
    if [[ $arg == exec ]]; then printf '%s\n' "$MOCK_PROFILE"; exit 0; fi
done
EOF
chmod +x "$TEST_ROOT/bin/docker"
export PATH="$TEST_ROOT/bin:$PATH"
export MOCK_DEPLOY_DIR="$TEST_ROOT/repo/deploy"
# Keep unrelated parent variables from overriding the .env fixture.
unset GATEWAY_OPENAI_CODEX_CLIENT_PROFILE GATEWAY_DISABLE_CODEX_IDENTITY_ENFORCEMENT GATEWAY_DISABLE_CODEX_ORIGINATOR_NORMALIZATION SITE_DOMAIN BIND_HOST JWT_SECRET TOTP_ENCRYPTION_KEY POSTGRES_PASSWORD REDIS_PASSWORD ADMIN_PASSWORD SUB2API_IMAGE SERVER_PORT COMPOSE_PROJECT_NAME || true
cases=0
pass() { cases=$((cases + 1)); }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
assert_contains() { grep -Fq -- "$2" "$1" || fail "missing $2 in $1"; }
run_ok() { bash "$SCRIPT" "$@" > "$TEST_ROOT/output" 2>&1 || { cat "$TEST_ROOT/output" >&2; fail 'startup unexpectedly failed'; }; }
run_fails() {
    local expected=$1 status=0; shift
    bash "$SCRIPT" "$@" > "$TEST_ROOT/output" 2>&1 || status=$?
    [[ $status == "$expected" ]] || { cat "$TEST_ROOT/output" >&2; fail "expected exit $expected, got $status"; }
}

# 1: help and unknown options never initialize a deployment.
run_ok --help
[[ ! -e $ENV_FILE ]] || fail 'help created .env'
run_fails 1 --unknown
[[ ! -e $ENV_FILE ]] || fail 'unknown option created .env'
pass

# 2: real OpenSSL initialization, explicit profile/domain, no Docker invocation.
run_ok --init-only --codex-aligned --domain api.example.com
[[ ! -e $MOCK_DOCKER_LOG ]] || fail 'init contacted Docker'
assert_contains "$ENV_FILE" 'GATEWAY_OPENAI_CODEX_CLIENT_PROFILE=codex-linux-rustls-0.153.4'
assert_contains "$ENV_FILE" 'SITE_DOMAIN=api.example.com'
assert_contains "$ENV_FILE" 'BIND_HOST=127.0.0.1'
assert_contains "$ENV_FILE" 'DATABASE_MAX_OPEN_CONNS=50'
assert_contains "$ENV_FILE" 'REDIS_POOL_SIZE=1024'
[[ $(grep -Ec '^(POSTGRES_PASSWORD|REDIS_PASSWORD|JWT_SECRET|TOTP_ENCRYPTION_KEY|ADMIN_PASSWORD)=[a-f0-9]{64}$' "$ENV_FILE") == 5 ]] || fail 'secrets were not generated'
[[ $(grep -E '^(POSTGRES_PASSWORD|REDIS_PASSWORD|JWT_SECRET|TOTP_ENCRYPTION_KEY|ADMIN_PASSWORD)=' "$ENV_FILE" | cut -d= -f2 | sort -u | wc -l) == 5 ]] || fail 'secrets reused'
while IFS= read -r secret; do
    if grep -Fq -- "$secret" "$TEST_ROOT/output"; then fail 'secret printed'; fi
done < <(grep -E '^(POSTGRES_PASSWORD|REDIS_PASSWORD|JWT_SECRET|TOTP_ENCRYPTION_KEY|ADMIN_PASSWORD)=' "$ENV_FILE" | cut -d= -f2)
pass

# 3: existing .env is not executed and is preserved exactly.
printf '\nNOT_A_SHELL=$(touch "%s")\n' "$TEST_ROOT/executed" >> "$ENV_FILE"
before=$(cksum "$ENV_FILE")
run_ok --init-only --codex-aligned --https
[[ $(cksum "$ENV_FILE") == "$before" ]] || fail 'existing .env changed'
[[ ! -e $TEST_ROOT/executed ]] || fail '.env was executed as shell'
[[ ! -e $MOCK_DOCKER_LOG ]] || fail 'init contacted Docker'
pass

# 4: production source build + optional HTTPS, config before build before up.
run_ok --codex-aligned --https
assert_contains "$MOCK_DOCKER_LOG" 'docker-compose.source.yml'
assert_contains "$MOCK_DOCKER_LOG" 'docker-compose.https.yml'
assert_contains "$MOCK_DOCKER_LOG" 'config --quiet'
assert_contains "$MOCK_DOCKER_LOG" 'build sub2api'
assert_contains "$MOCK_DOCKER_LOG" 'up -d --no-build --wait --wait-timeout 180'
assert_contains "$MOCK_DOCKER_LOG" 'exec -T sub2api printenv GATEWAY_OPENAI_CODEX_CLIENT_PROFILE'
assert_contains "$TEST_ROOT/output" 'Services healthy. Entry: https://api.example.com'
[[ $(cksum "$ENV_FILE") == "$before" ]] || fail 'startup changed existing .env'
config_line=$(grep -n 'config --quiet' "$MOCK_DOCKER_LOG" | head -1 | cut -d: -f1)
build_line=$(grep -n 'build sub2api' "$MOCK_DOCKER_LOG" | head -1 | cut -d: -f1)
up_line=$(grep -n 'up -d' "$MOCK_DOCKER_LOG" | head -1 | cut -d: -f1)
((config_line < build_line && build_line < up_line)) || fail 'incorrect deployment order'
pass

# 5: an explicitly selected prebuilt image does not build/pull official app.
: > "$MOCK_DOCKER_LOG"
run_ok --image registry.example.com/sub2api:release --https
assert_contains "$MOCK_DOCKER_LOG" 'selected-image=registry.example.com/sub2api:release'
if grep -Eq 'docker-compose.source.yml|build sub2api' "$MOCK_DOCKER_LOG"; then fail 'prebuilt mode built source'; fi
pass

# 6: no-build source mode uses the production overlay, without a build command.
: > "$MOCK_DOCKER_LOG"
run_ok --no-build
assert_contains "$MOCK_DOCKER_LOG" 'docker-compose.source.yml'
if grep -Fq 'build sub2api' "$MOCK_DOCKER_LOG"; then fail '--no-build built source'; fi
pass

# 7: config failure propagates and does not start/build services.
: > "$MOCK_DOCKER_LOG"
export MOCK_FAIL_STAGE=config MOCK_FAIL_CODE=7
run_fails 7
if grep -Eq 'build sub2api|up -d' "$MOCK_DOCKER_LOG"; then fail 'continued after config failure'; fi
pass

# 8: build failure propagates and does not start services.
: > "$MOCK_DOCKER_LOG"
export MOCK_FAIL_STAGE=build MOCK_FAIL_CODE=42
run_fails 42
if grep -Fq 'up -d' "$MOCK_DOCKER_LOG"; then fail 'continued after build failure'; fi
pass

# 9: failed readiness does not report healthy; runtime profile mismatch fails.
export MOCK_FAIL_STAGE=up MOCK_FAIL_CODE=81
run_fails 81 --no-build
if grep -Fq 'Services healthy.' "$TEST_ROOT/output"; then fail 'false readiness success'; fi
export MOCK_FAIL_STAGE="" MOCK_PROFILE=""
run_fails 1 --no-build
assert_contains "$TEST_ROOT/output" 'did not receive the selected Codex profile'
export MOCK_PROFILE=codex-linux-rustls-0.153.4
pass

# 10: mutually incompatible identity flags fail before Docker.
sed -i 's/^GATEWAY_DISABLE_CODEX_IDENTITY_ENFORCEMENT=false$/GATEWAY_DISABLE_CODEX_IDENTITY_ENFORCEMENT=true/' "$ENV_FILE"
: > "$MOCK_DOCKER_LOG"
run_fails 1
[[ ! -s $MOCK_DOCKER_LOG ]] || fail 'invalid profile reached Docker'
sed -i 's/^GATEWAY_DISABLE_CODEX_IDENTITY_ENFORCEMENT=true$/GATEWAY_DISABLE_CODEX_IDENTITY_ENFORCEMENT=false/' "$ENV_FILE"
pass

# 11: enabling on existing disabled config is explicit, no silent rewrite.
sed -i 's/^GATEWAY_OPENAI_CODEX_CLIENT_PROFILE=.*/GATEWAY_OPENAI_CODEX_CLIENT_PROFILE=/' "$ENV_FILE"
before=$(cksum "$ENV_FILE")
run_fails 1 --init-only --codex-aligned
[[ $(cksum "$ENV_FILE") == "$before" ]] || fail 'alignment rewrote existing config'
pass

# 12: invalid profile, domain, missing secrets and public app binding reject.
sed -i 's/^GATEWAY_OPENAI_CODEX_CLIENT_PROFILE=.*/GATEWAY_OPENAI_CODEX_CLIENT_PROFILE=codex-linux-latest/' "$ENV_FILE"
run_fails 1 --init-only
run_fails 1 --domain https://api.example.com
run_fails 1 --domain '-bad.example.com'
sed -i 's/^GATEWAY_OPENAI_CODEX_CLIENT_PROFILE=.*/GATEWAY_OPENAI_CODEX_CLIENT_PROFILE=/' "$ENV_FILE"
sed -i 's/^BIND_HOST=.*/BIND_HOST=0.0.0.0/' "$ENV_FILE"
run_fails 1 --init-only --https
sed -i 's/^BIND_HOST=.*/BIND_HOST=127.0.0.1/' "$ENV_FILE"
sed -i 's/^JWT_SECRET=.*/JWT_SECRET=/' "$ENV_FILE"
run_fails 1 --init-only
pass

# 13: runtime checks respect process environment over file values.
export JWT_SECRET=temporary-test-secret
run_ok --init-only
unset JWT_SECRET
pass

# 14: literal quoted/exported CRLF values and duplicate keys follow last value.
printf "export JWT_SECRET = 'literal-quoted-secret' # comment\r\n" >> "$ENV_FILE"
printf 'GATEWAY_OPENAI_CODEX_CLIENT_PROFILE=invalid-first\r\n' >> "$ENV_FILE"
printf 'GATEWAY_OPENAI_CODEX_CLIENT_PROFILE="" # explicit last value\r\n' >> "$ENV_FILE"
run_ok --init-only
pass

# 15: restored local data without original .env never creates new secrets.
cp "$ENV_FILE" "$TEST_ROOT/saved.env"
rm -- "$ENV_FILE"
printf 'persisted config\n' > "$TEST_ROOT/repo/deploy/data/config.yaml"
run_fails 1 --init-only
[[ ! -e $ENV_FILE ]] || fail 'restore generated replacement credentials'
rm -- "$TEST_ROOT/repo/deploy/data/config.yaml"
pass

# 16: exported initial secrets must not differ from secrets stored on disk.
export POSTGRES_PASSWORD=exported-secret
run_fails 1 --init-only
[[ ! -e $ENV_FILE ]] || fail 'exported secret created mismatched .env'
unset POSTGRES_PASSWORD
cp "$TEST_ROOT/saved.env" "$ENV_FILE"
pass

# 17: existing named volumes are not silently switched to empty bind mounts.
export MOCK_EXISTING_STORAGE=volume
: > "$MOCK_DOCKER_LOG"
run_fails 1 --no-build
if grep -Fq 'up -d' "$MOCK_DOCKER_LOG"; then fail 'changed named data mounts'; fi
run_ok --no-build --storage named
assert_contains "$MOCK_DOCKER_LOG" 'docker-compose.yml'
pass

# 18: a different bind source directory is a data-layout change too.
export MOCK_EXISTING_STORAGE=bind MOCK_EXISTING_SOURCE=/different/deployment/data
run_fails 1 --no-build
export MOCK_EXISTING_STORAGE="" MOCK_EXISTING_SOURCE=""
pass

# 19: retained named volumes are checked even when containers were removed.
export MOCK_RETAINED_VOLUME=deploy_sub2api_data
run_fails 1 --no-build
export MOCK_RETAINED_VOLUME=""
pass

# 20: duplicate loopback/public bindings validate the effective last value.
printf 'BIND_HOST=127.0.0.1\nBIND_HOST=0.0.0.0\n' >> "$ENV_FILE"
run_fails 1 --init-only --https
pass

# 21: normal startup with Docker data but no .env stops before generation.
rm -- "$ENV_FILE"
export MOCK_EXISTING_STORAGE=volume
run_fails 1
[[ ! -e $ENV_FILE ]] || fail 'existing Docker data generated fresh credentials'
export MOCK_EXISTING_STORAGE=""
pass

# 22: down/removed containers do not allow switching retained bind data to named.
cp "$TEST_ROOT/saved.env" "$ENV_FILE"
printf 'persisted database\n' > "$TEST_ROOT/repo/deploy/postgres_data/PG_VERSION"
run_fails 1 --init-only --storage named
rm -- "$TEST_ROOT/repo/deploy/postgres_data/PG_VERSION"
pass

# 23: labelled custom names are not the canonical volumes this Compose mounts.
export MOCK_RETAINED_VOLUME=production-db
run_fails 1 --no-build --storage named
export MOCK_RETAINED_VOLUME=""
pass

# 24: exact restored volume names are found even without Compose labels.
export MOCK_EXACT_VOLUMES=deploy_postgres_data
run_fails 1 --no-build
rm -- "$ENV_FILE"
run_fails 1
[[ ! -e $ENV_FILE ]] || fail 'unlabelled restored volume generated new secrets'
cp "$TEST_ROOT/saved.env" "$ENV_FILE"
export MOCK_EXACT_VOLUMES=""
pass

# 25: an existing container without the expected data destination must stop.
export MOCK_CONTAINER_WITHOUT_MOUNT=true
run_fails 1 --no-build
export MOCK_CONTAINER_WITHOUT_MOUNT=false
pass

# 26: inspect errors propagate rather than masquerading as absent mounts.
export MOCK_EXISTING_STORAGE=bind MOCK_INSPECT_FAIL=true
run_fails 19 --no-build
export MOCK_EXISTING_STORAGE="" MOCK_INSPECT_FAIL=false
pass

[[ ! -e $TEST_ROOT/repo/deploy/.start.lock ]] || fail 'lock leaked'
[[ ! -e $TEST_ROOT/executed ]] || fail 'dotenv shell injection executed'
printf 'one-click startup tests passed (%s cases; fake Docker, real secret generation)\n' "$cases"
