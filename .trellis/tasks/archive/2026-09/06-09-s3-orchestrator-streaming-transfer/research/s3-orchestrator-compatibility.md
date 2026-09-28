# s3-orchestrator compatibility notes

## Sources checked

* `https://raw.githubusercontent.com/afreidah/s3-orchestrator/main/README.md`
* `https://raw.githubusercontent.com/afreidah/s3-orchestrator/main/docs/user-guide.md`
* `https://raw.githubusercontent.com/afreidah/s3-orchestrator/main/internal/transport/s3api/buckets.go`
* `https://raw.githubusercontent.com/afreidah/s3-orchestrator/main/internal/transport/s3api/server.go`
* `https://raw.githubusercontent.com/afreidah/s3-orchestrator/main/internal/transport/s3api/buckets_test.go`

## Compatibility conclusion

PasteBox can treat s3-orchestrator as a normal path-style S3-compatible endpoint.

s3-orchestrator explicitly documents Go AWS SDK v2 usage with:

* `BaseEndpoint`
* static credentials
* `Region: "us-east-1"`
* `UsePathStyle: true`
* `PutObject`
* `GetObject`
* `DeleteObject`
* multipart support
* presigned URL support, although PasteBox does not use it in this task

The current PasteBox S3 config maps directly:

```env
PASTEBOX_S3_ENDPOINT=https://<s3-orchestrator-domain>
PASTEBOX_S3_BUCKET=<virtual-bucket>
PASTEBOX_S3_REGION=us-east-1
PASTEBOX_S3_ACCESS_KEY=<bucket-access-key>
PASTEBOX_S3_SECRET_KEY=<bucket-secret-key>
PASTEBOX_S3_USE_PATH_STYLE=true
```

## HeadBucket check

PasteBox readiness calls `HeadBucket`.

s3-orchestrator source has a bucket-level handler for `HEAD /{bucket}`:

* `handleHeadBucket` returns HTTP 200 with `X-Amz-Bucket-Region: us-east-1`.
* Router dispatches `method == http.MethodHead` with empty object key to operation `HeadBucket`.
* Tests include `TestHeadBucket` expecting HTTP 200 for the authorized bucket and `TestHeadBucketWrongBucket` expecting HTTP 403 for a wrong bucket.

So PasteBox readiness should work as long as:

* `PASTEBOX_S3_BUCKET` is the s3-orchestrator virtual bucket authorized for the credentials.
* `PASTEBOX_S3_USE_PATH_STYLE=true`.
* Production endpoint is exposed through HTTPS and a real domain, because PasteBox production preflight rejects local/HTTP object storage endpoints.

## Local verification in this task

The code-level S3 compatibility path is covered by `internal/objectstore` tests using a fake S3-compatible server that supports:

* `HEAD /bucket`
* `PUT /bucket/key`
* `GET /bucket/key`
* `DELETE /bucket/key`
* S3 XML not-found errors

The test now exercises the streaming path:

* `PutObjectStream`
* `OpenObject`
* legacy `GetObject` compatibility
* `DeleteObject`
* `Health` via `HeadBucket`

## Real s3-orchestrator PoC result

Ran a local s3-orchestrator v0.61.2 quickstart-style PoC:

* Cloned `https://github.com/afreidah/s3-orchestrator.git` to `/private/tmp/s3-orchestrator-poc`.
* Started its MinIO backend setup with `docker compose -f docker-compose.test.yml up -d minio-setup`.
* Started s3-orchestrator with project-local Go caches:

```sh
GOCACHE=/private/tmp/s3-orchestrator-poc/.gocache \
GOPATH=/private/tmp/s3-orchestrator-poc/.gopath \
go run ./cmd/s3-orchestrator -config config.yaml
```

* Verified PasteBox `S3Store` against that real endpoint:

```sh
GOCACHE=/Users/v/Documents/Code/Go/PasteBox/.cache/go-build \
GOPATH=/Users/v/Documents/Code/Go/PasteBox/.cache/gopath \
PASTEBOX_TEST_S3_ENDPOINT=http://localhost:9000 \
PASTEBOX_TEST_S3_BUCKET=photos \
PASTEBOX_TEST_S3_ACCESS_KEY=photoskey \
PASTEBOX_TEST_S3_SECRET_KEY=photossecret \
PASTEBOX_TEST_S3_REGION=us-east-1 \
go test ./internal/objectstore -run TestS3StoreExternalEndpoint -count=1 -v
```

Result:

```text
=== RUN   TestS3StoreExternalEndpoint
--- PASS: TestS3StoreExternalEndpoint (0.10s)
PASS
ok   pastebox/internal/objectstore 1.103s
```

The s3-orchestrator audit logs showed successful `HeadBucket`, `PutObject`, `GetObject`, `DeleteObject`, and post-delete `GetObject` 404 mapping. The local s3-orchestrator process was stopped and the quickstart Docker containers were brought down after verification.

## Production PoC checklist

When a real s3-orchestrator endpoint is available:

1. Set PasteBox S3 env vars to the s3-orchestrator virtual bucket credentials.
2. Run `pastebox preflight production`; endpoint must be HTTPS and a real domain.
3. Start API and worker.
4. Check `/readyz` or `/api/v1/ready` includes object storage ready.
5. Upload a normal user attachment and download it.
6. Upload a guest attachment and download it through a share.
7. Delete or expire the paste and verify the object disappears from s3-orchestrator metadata/backends.
8. If backend quotas are configured, set a tiny backend quota and verify new uploads route to the next backend.
