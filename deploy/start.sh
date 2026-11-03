#!/usr/bin/env bash
# Local-source production deployment. Does not download or overwrite config.
set -euo pipefail

DEPLOY_DIR=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
ROOT_DIR=$(CDPATH= cd -- "$DEPLOY_DIR/.." && pwd)
ENV_FILE="$DEPLOY_DIR/.env"
PROFILE=codex-linux-rustls-0.153.4
init_only=false
codex_aligned=false
https=false
no_build=false
storage=local
docker_checked=false
image=""
domain=""
lock_owned=false
pending=""

fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
usage() {
    cat <<'EOF'
Usage: bash deploy/start.sh [options]
  --init-only       Create .env with generated secrets; do not contact Docker
  --codex-aligned   Enable fixed Codex profile in a NEW .env (verify existing)
  --https           Add Caddy HTTPS; SITE_DOMAIN must be set
  --domain DOMAIN   Domain for a NEW .env; also enables --https
  --image IMAGE     Use a prebuilt image instead of building current source
  --no-build        Use an already built/loaded SUB2API_SOURCE_IMAGE
  --storage LAYOUT  local (default) or named; never silently change data mounts
  --help            Show this help
Existing .env is preserved byte-for-byte. See deploy/ONE_CLICK_CN.md.
EOF
}
while (($#)); do
    case "$1" in
        --init-only) init_only=true ;;
        --codex-aligned) codex_aligned=true ;;
        --https) https=true ;;
        --no-build) no_build=true ;;
        --domain|--image|--storage)
            option=$1
            (($# >= 2)) || fail "$option requires a value"
            [[ -n $2 && $2 != --* ]] || fail "$option requires a value"
            case "$option" in
                --domain) domain=$2; https=true ;;
                --image) image=$2 ;;
                --storage) storage=$2 ;;
            esac
            shift ;;
        --help|-h) usage; exit 0 ;;
        *) fail "unknown option: $1" ;;
    esac
    shift
done

valid_domain() {
    local value=$1 label
    [[ ${#value} -le 253 && $value == *.* && $value != *..* ]] || return 1
    local IFS=.
    local -a labels
    read -r -a labels <<< "$value"
    for label in "${labels[@]}"; do
        [[ ${#label} -ge 1 && ${#label} -le 63 && $label =~ ^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?$ ]] || return 1
    done
    [[ $value != *. ]]
}
[[ $storage == local || $storage == named ]] || fail "--storage must be local or named"
[[ -z $domain ]] || valid_domain "$domain" || fail '--domain must be a DNS hostname, not a URL'
[[ -z $image || $image =~ ^[A-Za-z0-9][A-Za-z0-9_./:@-]*$ ]] || fail 'invalid image name'

# Read literal dotenv values without executing .env as a shell script.
# Standard KEY=value, optional export, quotes, whitespace and CRLF are accepted.
read_env() {
    local key=$1 line value found=""
    if [[ -v $key ]]; then printf '%s' "${!key}"; return; fi
    while IFS= read -r line || [[ -n $line ]]; do
        line=${line%$'\r'}
        if [[ $line =~ ^[[:space:]]*(export[[:space:]]+)?${key}[[:space:]]*=(.*)$ ]]; then
            value=${BASH_REMATCH[2]}
            value="${value#"${value%%[![:space:]]*}"}"
            if [[ $value == \'* ]]; then
                value=${value#\'}
                value=${value%%\'*}
            elif [[ $value == \"* ]]; then
                value=${value#\"}
                value=${value%%\"*}
            else
                value=${value%%[[:space:]]#*}
                value="${value%"${value##*[![:space:]]}"}"
            fi
            found=$value
        fi
    done < "$ENV_FILE"
    printf '%s' "$found"
}

cleanup() {
    [[ -z $pending ]] || rm -f -- "$pending"
    if $lock_owned; then rmdir -- "$DEPLOY_DIR/.start.lock"; fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir -- "$DEPLOY_DIR/.start.lock" 2>/dev/null || fail 'another startup owns deploy/.start.lock'
lock_owned=true

docker_preflight() {
    if $docker_checked; then return; fi
    command -v docker >/dev/null || fail 'Docker Engine and Compose v2 are required'
    docker compose version >/dev/null
    docker info >/dev/null
    docker_checked=true
}

has_line() {
    local text=$1 expected=$2 line
    while IFS= read -r line; do
        [[ $line != "$expected" ]] || return 0
    done <<< "$text"
    return 1
}

local_data_present() {
    local directory
    local -a entries
    shopt -s nullglob dotglob
    for directory in data postgres_data redis_data; do
        entries=("$DEPLOY_DIR/$directory"/*)
        if ((${#entries[@]})); then shopt -u nullglob dotglob; return 0; fi
    done
    shopt -u nullglob dotglob
    return 1
}

check_existing_storage() {
    local name destination directory volume mount type source mounted_name project retained expected retained_name
    local container_names volume_names
    project=${COMPOSE_PROJECT_NAME:-}
    if [[ -f $ENV_FILE ]]; then project=$(read_env COMPOSE_PROJECT_NAME); fi
    [[ -n $project ]] || project=$(basename -- "$DEPLOY_DIR")
    # Listing errors are fatal, rather than being confused with absent containers.
    container_names=$(docker ps -a --format '{{.Names}}')
    volume_names=$(docker volume ls -q)
    for name in sub2api sub2api-postgres sub2api-redis; do
        case "$name" in
            sub2api) destination=/app/data; directory=data; volume=sub2api_data ;;
            sub2api-postgres) destination=/var/lib/postgresql/data; directory=postgres_data; volume=postgres_data ;;
            sub2api-redis) destination=/data; directory=redis_data; volume=redis_data ;;
        esac
        if has_line "$container_names" "$name"; then
            [[ -f $ENV_FILE ]] || fail 'existing containers/data found; restore the original .env before initialization'
            mount=$(docker inspect --type container --format "{{range .Mounts}}{{if eq .Destination \"$destination\"}}{{.Type}}|{{.Source}}|{{.Name}}{{end}}{{end}}" "$name")
            [[ -n $mount ]] || fail "existing $name has no expected persistent data mount; retain its original deployment"
            IFS='|' read -r type source mounted_name <<< "$mount"
            if [[ $storage == local ]]; then
                [[ $type == bind && $source == "$DEPLOY_DIR/$directory" ]] || fail "existing $name uses other data mounts; retain --storage named or use the original deployment directory"
            else
                [[ $type == volume && $mounted_name == "${project}_$volume" ]] || fail "existing $name does not match this named-volume layout; retain the original deployment"
            fi
        fi
        expected="${project}_$volume"
        retained=$(docker volume ls -q --filter "label=com.docker.compose.project=$project" --filter "label=com.docker.compose.volume=$volume")
        if [[ -n $retained ]] || has_line "$volume_names" "$expected"; then
            [[ -f $ENV_FILE ]] || fail 'retained database/application volumes found; restore original .env'
            [[ $storage == named ]] || fail 'retained named data volumes found; use --storage named instead of changing data layout'
            while IFS= read -r retained_name; do
                [[ -z $retained_name || $retained_name == "$expected" ]] || fail 'retained volume has a noncanonical name; retain its original Compose volume mapping'
            done <<< "$retained"
        fi
    done
}

if [[ $storage == named ]] && local_data_present; then
    fail 'persisted local data found; retain local storage instead of switching to named volumes'
fi

if [[ ! -f $ENV_FILE ]]; then
    # Data without its original credentials is a restore, not a fresh install.
    if local_data_present; then fail 'persisted data found without .env; restore original credentials before initialization'; fi
    for key in POSTGRES_PASSWORD REDIS_PASSWORD JWT_SECRET TOTP_ENCRYPTION_KEY ADMIN_PASSWORD; do
        [[ ! -v $key ]] || fail "unset exported $key before creating .env so stored and deployed credentials agree"
    done
    if ! $init_only; then docker_preflight; check_existing_storage; fi
    command -v openssl >/dev/null || fail 'openssl is required to initialize .env'
    umask 077
    pending=$(mktemp "$DEPLOY_DIR/.env.init.XXXXXX")
    declare -A values=(
        [BIND_HOST]=127.0.0.1
        [DATABASE_MAX_OPEN_CONNS]=50 [DATABASE_MAX_IDLE_CONNS]=10
        [REDIS_POOL_SIZE]=1024 [REDIS_MIN_IDLE_CONNS]=10
        [GATEWAY_MAX_CONNS_PER_HOST]=1024 [GATEWAY_MAX_IDLE_CONNS]=2560
        [GATEWAY_MAX_IDLE_CONNS_PER_HOST]=120
    )
    for key in POSTGRES_PASSWORD REDIS_PASSWORD JWT_SECRET TOTP_ENCRYPTION_KEY ADMIN_PASSWORD; do
        values[$key]=$(openssl rand -hex 32)
    done
    if $codex_aligned; then values[GATEWAY_OPENAI_CODEX_CLIENT_PROFILE]=$PROFILE; fi
    if [[ -n $domain ]]; then
        values[SITE_DOMAIN]=$domain
        values[SERVER_FRONTEND_URL]="https://$domain"
    fi
    if [[ -n $image ]]; then values[SUB2API_IMAGE]=$image; fi
    while IFS= read -r line || [[ -n $line ]]; do
        line=${line%$'\r'}
        if [[ $line =~ ^([A-Z][A-Z0-9_]*)= ]]; then
            key=${BASH_REMATCH[1]}
            if [[ -v values[$key] ]]; then line="$key=${values[$key]}"; fi
        fi
        printf '%s\n' "$line"
    done < "$DEPLOY_DIR/.env.example" > "$pending"
    # Refuse clobber even if another tool created .env after our existence check.
    (set -o noclobber; cat -- "$pending" > "$ENV_FILE") || fail '.env was created by another process'
    chmod 600 "$ENV_FILE"
    printf 'Created deploy/.env; generated credentials remain in that file.\n'
else
    printf 'Preserved existing deploy/.env.\n'
fi

for key in POSTGRES_PASSWORD JWT_SECRET TOTP_ENCRYPTION_KEY; do
    [[ -n $(read_env "$key") ]] || fail "set $key in deploy/.env before startup"
done
profile=$(read_env GATEWAY_OPENAI_CODEX_CLIENT_PROFILE)
[[ -z $profile || $profile == "$PROFILE" ]] || fail 'unsupported Codex profile; use empty or codex-linux-rustls-0.153.4'
if $codex_aligned && [[ $profile != "$PROFILE" ]]; then
    fail '--codex-aligned preserves existing .env; set GATEWAY_OPENAI_CODEX_CLIENT_PROFILE=codex-linux-rustls-0.153.4 there first'
fi
if [[ -n $profile ]]; then
    for key in GATEWAY_DISABLE_CODEX_IDENTITY_ENFORCEMENT GATEWAY_DISABLE_CODEX_ORIGINATOR_NORMALIZATION; do
        value=$(read_env "$key")
        [[ -z $value || $value == false ]] || fail "$key must be false when Codex profile is enabled"
    done
fi
if $https; then
    effective_domain=$(read_env SITE_DOMAIN)
    valid_domain "$effective_domain" || fail 'set SITE_DOMAIN to a DNS hostname in deploy/.env'
    [[ -z $domain || $effective_domain == "$domain" ]] || fail '--domain differs from existing .env; edit SITE_DOMAIN explicitly'
    bind_host=$(read_env BIND_HOST)
    [[ $bind_host == 127.0.0.1 || $bind_host == ::1 ]] || fail 'HTTPS requires BIND_HOST=127.0.0.1 or ::1'
fi
if [[ $storage == local ]]; then
    mkdir -p -- "$DEPLOY_DIR/data" "$DEPLOY_DIR/postgres_data" "$DEPLOY_DIR/redis_data"
fi
if $init_only; then printf 'Initialization complete; Docker was not contacted.\n'; exit 0; fi

docker_preflight
check_existing_storage
base_file=docker-compose.local.yml
if [[ $storage == named ]]; then base_file=docker-compose.yml; fi
compose=(docker compose --project-directory "$DEPLOY_DIR" --env-file "$ENV_FILE" -f "$DEPLOY_DIR/$base_file")
if [[ -n $image ]]; then
    export SUB2API_IMAGE=$image
else
    compose+=(-f "$DEPLOY_DIR/docker-compose.source.yml")
    [[ -f $ROOT_DIR/Dockerfile ]] || fail 'root Dockerfile is required for source deployment'
fi
if $https; then compose+=(-f "$DEPLOY_DIR/docker-compose.https.yml"); fi
"${compose[@]}" config --quiet
if [[ -z $image ]] && ! $no_build; then "${compose[@]}" build sub2api; fi
"${compose[@]}" up -d --no-build --wait --wait-timeout 180
"${compose[@]}" ps
if [[ -n $profile ]]; then
    actual=$("${compose[@]}" exec -T sub2api printenv GATEWAY_OPENAI_CODEX_CLIENT_PROFILE)
    actual=${actual%$'\r'}
    [[ $actual == "$profile" ]] || fail 'running container did not receive the selected Codex profile'
    printf 'Codex profile verified in running container: %s\n' "$profile"
fi
if $https; then
    printf 'Services healthy. Entry: https://%s\n' "$effective_domain"
else
    printf 'Services healthy. Local entry: http://127.0.0.1:%s\n' "$(read_env SERVER_PORT)"
fi
printf 'Administrator credentials: deploy/.env. Business acceptance checklist: deploy/ONE_CLICK_CN.md.\n'
