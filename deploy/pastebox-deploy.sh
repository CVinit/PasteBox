#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_root"

env_file=${PASTEBOX_ENV_FILE:-deploy/production.env}
infra_env_file=${PASTEBOX_SHARED_ENV_FILE:-deploy/shared-services.env}
mode=${PASTEBOX_DEPLOY_MODE:-shared}
host_override=${PASTEBOX_COMPOSE_OVERRIDE:-}
if [ -z "$host_override" ] && [ -f compose.nginx-host.yaml ]; then
	host_override=compose.nginx-host.yaml
fi

usage() {
	cat <<'EOF'
用法: ./deploy/pastebox-deploy.sh <命令> [参数]

命令:
  init                 首次初始化；共享模式会启动 PostgreSQL/Redis 并创建 PasteBox 数据库
  up                   拉取镜像、迁移并启动 PasteBox
  status               查看容器状态
  logs [service...]    查看日志，默认 api 和 worker
  upgrade              拉取新镜像、迁移并滚动更新 PasteBox
  down                 停止 PasteBox；共享 PostgreSQL/Redis 保持运行
  preflight-root       运行首次启动根配置检查
  preflight            运行完整生产配置检查
  admin EMAIL PASSWORD 创建或重置管理员
  infra-status         查看共享 PostgreSQL/Redis
  infra-down           停止共享 PostgreSQL/Redis
  infra-reset --confirm-delete-all-data
                       停止并删除全部共享数据卷（危险）
  compose <args...>    透传到最终 Compose 配置

环境变量:
  PASTEBOX_DEPLOY_MODE=shared|integrated   默认 shared
  PASTEBOX_ENV_FILE=<path>                 默认 deploy/production.env
  PASTEBOX_SHARED_ENV_FILE=<path>          默认 deploy/shared-services.env
  PASTEBOX_COMPOSE_OVERRIDE=<path>         可选，例如 compose.nginx-host.yaml
EOF
}

die() {
	printf '错误: %s\n' "$*" >&2
	exit 1
}

require_file() {
	[ -f "$1" ] || die "缺少文件 $1"
}

env_value() {
	file=$1
	key=$2
	awk -F= -v key="$key" '$1 == key { sub(/^[^=]*=/, ""); print; exit }' "$file"
}

compose() {
	docker compose "$@"
}

infra_compose() {
	docker compose --env-file "$infra_env_file" -f compose.shared-services.yaml "$@"
}

build_compose_args() {
	set -- --env-file "$env_file" -f compose.production.yaml
	if [ "$mode" = "shared" ]; then
		set -- "$@" --env-file "$infra_env_file" -f compose.external-services.yaml
	fi
	if [ -n "$host_override" ]; then
		set -- "$@" -f "$host_override"
	fi
	COMPOSE_ARGS=$*
}

app_compose() {
	# Compose paths cannot contain spaces in this deployment layout.
	# shellcheck disable=SC2086
	compose $COMPOSE_ARGS "$@"
}

wait_for_shared_postgres() {
	superuser=$(env_value "$infra_env_file" SHARED_POSTGRES_SUPERUSER)
	[ -n "$superuser" ] || superuser=postgres
	i=0
	while [ "$i" -lt 60 ]; do
		if infra_compose exec -T postgres pg_isready -U "$superuser" -d postgres >/dev/null 2>&1; then
			return 0
		fi
		i=$((i + 1))
		sleep 2
	done
	die "共享 PostgreSQL 在 120 秒内未就绪"
}

init_shared_database() {
	superuser=$(env_value "$infra_env_file" SHARED_POSTGRES_SUPERUSER)
	[ -n "$superuser" ] || superuser=postgres
	database_url=$(env_value "$env_file" PASTEBOX_DATABASE_URL)
	case "$database_url" in
		postgres://pastebox@shared-postgres:5432/pastebox*) ;;
		*) die "共享模式下 PASTEBOX_DATABASE_URL 应连接 shared-postgres:5432/pastebox，并使用 pastebox 独立账号" ;;
	esac
	password=$(env_value "$env_file" PASTEBOX_POSTGRES_PASSWORD)
	case "$password" in
		""|*"'"*) die "PasteBox 数据库密码不能为空，也不能含单引号" ;;
	esac

	infra_compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$superuser" -d postgres \
		-v app_password="$password" <<'SQL'
SELECT format('CREATE ROLE pastebox LOGIN PASSWORD %L', :'app_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'pastebox') \gexec
SELECT format('ALTER ROLE pastebox LOGIN PASSWORD %L', :'app_password') \gexec
SELECT 'CREATE DATABASE pastebox OWNER pastebox'
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'pastebox') \gexec
ALTER DATABASE pastebox OWNER TO pastebox;
REVOKE ALL ON DATABASE pastebox FROM PUBLIC;
GRANT CONNECT, TEMPORARY ON DATABASE pastebox TO pastebox;
SQL
}

command_name=${1:-help}
if [ "$#" -gt 0 ]; then
	shift
fi

case "$mode" in
	shared|integrated) ;;
	*) die "PASTEBOX_DEPLOY_MODE 只能是 shared 或 integrated" ;;
esac

case "$command_name" in
	help|-h|--help)
		usage
		exit 0
		;;
	infra-status|infra-down|infra-reset)
		require_file "$infra_env_file"
		case "$command_name" in
			infra-status)
				infra_compose ps
				;;
			infra-down)
				infra_compose down
				;;
			infra-reset)
				[ "${1:-}" = "--confirm-delete-all-data" ] || die "危险操作：使用 infra-reset --confirm-delete-all-data 才会删除共享数据库和 Redis 数据卷"
				infra_compose down -v --remove-orphans
				backup_volume=$(env_value "$infra_env_file" SHARED_BACKUP_VOLUME)
				[ -n "$backup_volume" ] || backup_volume=shared-postgres-backups
				docker volume rm "$backup_volume" >/dev/null 2>&1 || true
				;;
		esac
		exit 0
		;;
	init|up|upgrade|status|logs|down|preflight-root|preflight|admin|compose) ;;
	*)
		usage >&2
		die "未知命令 $command_name"
		;;
esac

require_file "$env_file"
[ -z "$host_override" ] || require_file "$host_override"
if [ "$mode" = "shared" ]; then
	require_file "$infra_env_file"
fi
export PASTEBOX_ENV_FILE="$env_file"
build_compose_args

case "$command_name" in
	init)
		if [ "$mode" = "shared" ]; then
			infra_compose config --quiet
			backup_volume=$(env_value "$infra_env_file" SHARED_BACKUP_VOLUME)
			[ -n "$backup_volume" ] || backup_volume=shared-postgres-backups
			docker volume create "$backup_volume" >/dev/null
			infra_compose up -d postgres redis
			wait_for_shared_postgres
			init_shared_database
			printf '共享 PostgreSQL/Redis 和 PasteBox 独立数据库已就绪。\n'
		else
			app_compose config --quiet
			app_compose up -d postgres redis
			printf 'PasteBox 内置 PostgreSQL/Redis 已就绪。\n'
		fi
		;;
	up)
		app_compose config --quiet
		app_compose pull api worker preflight migrate
		app_compose --profile maintenance run --rm migrate
		app_compose up -d clamav api worker
		;;
	upgrade)
		app_compose config --quiet
		app_compose pull api worker preflight migrate
		app_compose --profile maintenance run --rm migrate
		app_compose up -d --no-deps api worker
		;;
	status)
		app_compose ps
		;;
	logs)
		if [ "$#" -eq 0 ]; then
			set -- api worker
		fi
		app_compose logs --tail=200 -f "$@"
		;;
	down)
		app_compose down --remove-orphans
		;;
	preflight-root)
		PASTEBOX_PREFLIGHT_ROOT_ONLY=true app_compose --profile maintenance run --rm preflight
		;;
	preflight)
		app_compose --profile maintenance run --rm preflight
		;;
	admin)
		email=${1:-}
		password=${2:-}
		[ -n "$email" ] || die "用法: ./deploy/pastebox-deploy.sh admin EMAIL PASSWORD"
		[ -n "$password" ] || die "用法: ./deploy/pastebox-deploy.sh admin EMAIL PASSWORD"
		app_compose run --rm api admin create --email "$email" --password "$password"
		;;
	compose)
		app_compose "$@"
		;;
esac
