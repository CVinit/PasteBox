# PostgreSQL / Redis deployment naming

## Request
Review docs/shared-pg-redis-deployment.zh-CN.md and its deployment dependencies. Replace infrastructure shared-prefixed names with postgresql / redis consistently, including equivalent naming in related files.

## Decisions
- Projects and external aliases: postgresql and redis. Networks: postgresql-net / redis-net. Volumes: postgresql-data / redis-data / postgresql-backups.
- Rename compose.shared-postgres.yaml -> compose.postgresql.yaml, compose.shared-redis.yaml -> compose.redis.yaml, compose.shared-services.yaml -> compose.infra.yaml.
- Rename deploy/shared-{postgres,redis,services}.env.example -> deploy/{postgresql,redis,infra}.env.example; production.shared.env.example -> production.combined.env.example. Keep production.split.env.example.
- Rename tutorial to docs/postgresql-redis-deployment.zh-CN.md and update all current links (historical journals/tasks unchanged).
- Replace SHARED_POSTGRES_* -> POSTGRESQL_*, SHARED_REDIS_* -> REDIS_*, SHARED_BACKUP_VOLUME -> POSTGRESQL_BACKUP_VOLUME; combined infra project/network use INFRA_COMPOSE_PROJECT_NAME / INFRA_NETWORK_SUBNET and infra / infra-net.
- Replace deployment modes shared/shared-split with combined/split; preserve default topology (combined) and integrated mode.
- Replace PASTEBOX_SHARED_ENV_FILE -> PASTEBOX_INFRA_ENV_FILE; PASTEBOX_SHARED_POSTGRES_* -> PASTEBOX_POSTGRESQL_*; PASTEBOX_SHARED_REDIS_* -> PASTEBOX_REDIS_*.
- Preserve official postgres images, PostgreSQL standard POSTGRES_* / PG* environment names, Compose postgres service key, and application PASTEBOX_POSTGRES_PASSWORD.
- No active backward aliases: explicitly document breaking configuration migration and safe reuse of existing volumes through new override variables. Never operate on real containers, networks or volumes.
- Fix target tutorial to use production.split.env.example and remove obsolete compatibility explanations. Ensure standalone compose project names agree with tutorial -p names.
- Update deployment script, readiness checks, related docs and current Trellis specs atomically. Do not modify historical records, Go/frontend business logic, or unrelated existing changes.
- Protect renamed real env files in .gitignore/.dockerignore. Correct readiness sh -n to check each script individually; add focused naming/config assertions where useful.

## Acceptance
- [x] No active old shared infrastructure identifiers except historical migration explanation.
- [x] Both external layouts render with maintenance and Nginx override; integrated layout still renders.
- [x] Alias/network/volume/config-variable contracts agree across producers, consumers and wrapper.
- [x] Wrapper new modes render and old/invalid modes fail clearly; application commands do not manage external infra.
- [x] Shell syntax checks and git diff --check pass. Report runtime checks not performed.
