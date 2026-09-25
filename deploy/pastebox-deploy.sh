#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_root"

env_file=${PASTEBOX_ENV_FILE:-deploy/production.env}
mode=${PASTEBOX_DEPLOY_MODE:-split}
split_pg_env_file=${PASTEBOX_POSTGRESQL_ENV_FILE:-deploy/postgresql.env}
split_redis_env_file=${PASTEBOX_REDIS_ENV_FILE:-deploy/redis.env}
host_override=${PASTEBOX_COMPOSE_OVERRIDE:-}
if [ -z "$host_override" ] && [ -f compose.nginx-host.yaml ]; then
	host_override=compose.nginx-host.yaml
fi

usage() {
	cat <<'EOF'
用法: ./deploy/pastebox-deploy.sh <命令> [参数]

命令:
  up                   拉取镜像、迁移并启动 PasteBox
  status               查看容器状态
  logs [service...]    查看日志，默认 api 和 worker
  upgrade              拉取新镜像、迁移并滚动更新 PasteBox
  down                 停止 PasteBox；独立 PostgreSQL/Redis 不受影响
  preflight-root       运行首次启动根配置检查
  preflight            运行完整生产配置检查
  admin EMAIL PASSWORD 创建或重置管理员
  compose <args...>    透传到最终 Compose 配置

环境变量:
  PASTEBOX_DEPLOY_MODE=split              当前仅支持独立 PostgreSQL/Redis 模式
  PASTEBOX_ENV_FILE=<path>                 默认 deploy/production.env
  PASTEBOX_POSTGRESQL_ENV_FILE=<path>     默认 deploy/postgresql.env
  PASTEBOX_REDIS_ENV_FILE=<path>         split 模式默认 deploy/redis.env
  PASTEBOX_COMPOSE_OVERRIDE=<path>         可选，例如 compose.nginx-host.yaml

PostgreSQL 和 Redis 独立目录部署时（split 模式），例如：
  PASTEBOX_POSTGRESQL_ENV_FILE=/opt/postgresql/.env \
  PASTEBOX_REDIS_ENV_FILE=/opt/redis/.env
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

compose() {
	docker compose "$@"
}

build_compose_args() {
	set -- --env-file "$env_file" --env-file "$split_pg_env_file" --env-file "$split_redis_env_file" \
		-f compose.production.yaml -f compose.external-split-services.yaml
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

command_name=${1:-help}
if [ "$#" -gt 0 ]; then
	shift
fi

case "$mode" in
	split) ;;
	*) die "PASTEBOX_DEPLOY_MODE 只能是 split" ;;
esac

require_split_files() {
	require_file "$split_pg_env_file"
	require_file "$split_redis_env_file"
}

case "$command_name" in
	help|-h|--help)
		usage
		exit 0
		;;
	up|upgrade|status|logs|down|preflight-root|preflight|admin|compose) ;;
	*)
		usage >&2
		die "未知命令 $command_name"
		;;
esac

require_file "$env_file"
require_split_files
[ -z "$host_override" ] || require_file "$host_override"
export PASTEBOX_ENV_FILE="$env_file"
build_compose_args

case "$command_name" in
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
