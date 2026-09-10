#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_root"

env_file=${PASTEBOX_ENV_FILE:-deploy/production.env}
infra_env_file=${PASTEBOX_SHARED_ENV_FILE:-deploy/shared-services.env}
mode=${PASTEBOX_DEPLOY_MODE:-shared}
split_pg_compose_file=${PASTEBOX_SHARED_POSTGRES_COMPOSE_FILE:-compose.shared-postgres.yaml}
split_pg_env_file=${PASTEBOX_SHARED_POSTGRES_ENV_FILE:-deploy/shared-postgres.env}
split_redis_compose_file=${PASTEBOX_SHARED_REDIS_COMPOSE_FILE:-compose.shared-redis.yaml}
split_redis_env_file=${PASTEBOX_SHARED_REDIS_ENV_FILE:-deploy/shared-redis.env}
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
  PASTEBOX_DEPLOY_MODE=shared|shared-split|integrated   默认 shared
  PASTEBOX_ENV_FILE=<path>                 默认 deploy/production.env
  PASTEBOX_SHARED_ENV_FILE=<path>          shared 模式默认 deploy/shared-services.env
  PASTEBOX_SHARED_POSTGRES_COMPOSE_FILE=<path> shared-split 模式默认 compose.shared-postgres.yaml
  PASTEBOX_SHARED_POSTGRES_ENV_FILE=<path>    shared-split 模式默认 deploy/shared-postgres.env
  PASTEBOX_SHARED_REDIS_COMPOSE_FILE=<path>   shared-split 模式默认 compose.shared-redis.yaml
  PASTEBOX_SHARED_REDIS_ENV_FILE=<path>       shared-split 模式默认 deploy/shared-redis.env
  PASTEBOX_COMPOSE_OVERRIDE=<path>         可选，例如 compose.nginx-host.yaml

共享服务独立目录部署时（推荐），例如：
  PASTEBOX_SHARED_POSTGRES_COMPOSE_FILE=/opt/shared-postgres/compose.yaml \
  PASTEBOX_SHARED_POSTGRES_ENV_FILE=/opt/shared-postgres/.env \
  PASTEBOX_SHARED_REDIS_COMPOSE_FILE=/opt/shared-redis/compose.yaml \
  PASTEBOX_SHARED_REDIS_ENV_FILE=/opt/shared-redis/.env
可以把这些变量写入一个文件后 source，或放到 cron/服务管理器的环境里。
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

split_pg_compose() {
	docker compose --env-file "$split_pg_env_file" -f "$split_pg_compose_file" "$@"
}

split_redis_compose() {
	docker compose --env-file "$split_redis_env_file" -f "$split_redis_compose_file" "$@"
}

build_compose_args() {
	set -- --env-file "$env_file" -f compose.production.yaml
	if [ "$mode" = "shared" ]; then
		set -- "$@" --env-file "$infra_env_file" -f compose.external-services.yaml
	elif [ "$mode" = "shared-split" ]; then
		set -- "$@" --env-file "$split_pg_env_file" --env-file "$split_redis_env_file" -f compose.external-split-services.yaml
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

wait_for_split_postgres() {
	superuser=$(env_value "$split_pg_env_file" SHARED_POSTGRES_SUPERUSER)
	[ -n "$superuser" ] || superuser=postgres
	i=0
	while [ "$i" -lt 60 ]; do
		if split_pg_compose exec -T postgres pg_isready -U "$superuser" -d postgres >/dev/null 2>&1; then
			return 0
		fi
		i=$((i + 1))
		sleep 2
	done
	die "共享 PostgreSQL 在 120 秒内未就绪"
}

init_shared_database() {
	case "$mode" in
		shared) init_shared_database_on infra_compose "$infra_env_file" ;;
		shared-split) init_shared_database_on split_pg_compose "$split_pg_env_file" ;;
	esac
}

init_shared_database_on() {
	compose_cmd=$1
	infra_env=$2
	superuser=$(env_value "$infra_env" SHARED_POSTGRES_SUPERUSER)
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

	$compose_cmd exec -T postgres psql -v ON_ERROR_STOP=1 -U "$superuser" -d postgres \
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
	shared|shared-split|integrated) ;;
	*) die "PASTEBOX_DEPLOY_MODE 只能是 shared、shared-split 或 integrated" ;;
esac

require_split_files() {
	require_file "$split_pg_compose_file"
	require_file "$split_pg_env_file"
	require_file "$split_redis_compose_file"
	require_file "$split_redis_env_file"
}

case "$command_name" in
	help|-h|--help)
		usage
		exit 0
		;;
	infra-status|infra-down|infra-reset)
		case "$mode" in
			shared) require_file "$infra_env_file" ;;
			shared-split) require_split_files ;;
		esac
		case "$command_name" in
			infra-status)
				case "$mode" in
					shared) infra_compose ps ;;
					shared-split)
						split_pg_compose ps
						split_redis_compose ps
						;;
				esac
				;;
			infra-down)
				case "$mode" in
					shared) infra_compose down ;;
					shared-split)
						split_pg_compose down
						split_redis_compose down
						;;
				esac
				;;
			infra-reset)
				[ "${1:-}" = "--confirm-delete-all-data" ] || die "危险操作：使用 infra-reset --confirm-delete-all-data 才会删除共享数据库和 Redis 数据卷"
				case "$mode" in
					shared)
						infra_compose down -v --remove-orphans
						backup_volume=$(env_value "$infra_env_file" SHARED_BACKUP_VOLUME)
						[ -n "$backup_volume" ] || backup_volume=shared-postgres-backups
						docker volume rm "$backup_volume" >/dev/null 2>&1 || true
						;;
					shared-split)
						split_pg_compose down -v --remove-orphans
						split_redis_compose down -v --remove-orphans
						backup_volume=$(env_value "$split_pg_env_file" SHARED_BACKUP_VOLUME)
						[ -n "$backup_volume" ] || backup_volume=shared-postgres-backups
						docker volume rm "$backup_volume" >/dev/null 2>&1 || true
						;;
				esac
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
case "$mode" in
	shared) require_file "$infra_env_file" ;;
	shared-split) require_split_files ;;
esac
export PASTEBOX_ENV_FILE="$env_file"
build_compose_args

case "$command_name" in
	init)
		case "$mode" in
			shared)
				infra_compose config --quiet
				backup_volume=$(env_value "$infra_env_file" SHARED_BACKUP_VOLUME)
				[ -n "$backup_volume" ] || backup_volume=shared-postgres-backups
				docker volume create "$backup_volume" >/dev/null
				infra_compose up -d postgres redis
				wait_for_shared_postgres
				init_shared_database
				printf '共享 PostgreSQL/Redis 和 PasteBox 独立数据库已就绪。\n'
				;;
			shared-split)
				split_pg_compose config --quiet
				split_redis_compose config --quiet
				backup_volume=$(env_value "$split_pg_env_file" SHARED_BACKUP_VOLUME)
				[ -n "$backup_volume" ] || backup_volume=shared-postgres-backups
				docker volume create "$backup_volume" >/dev/null
				split_pg_compose up -d postgres
				split_redis_compose up -d redis
				wait_for_split_postgres
				init_shared_database
				printf '共享 PostgreSQL、Redis 和 PasteBox 独立数据库已就绪。\n'
				;;
			*)
				app_compose config --quiet
				app_compose up -d postgres redis
				printf 'PasteBox 内置 PostgreSQL/Redis 已就绪。\n'
				;;
		esac
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
