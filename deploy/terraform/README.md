# Deployment

Terraform for running the transaction ingest service on AWS ECS Fargate.

> ⚠️ **Nothing here has been applied.** It is written, formatted, and validated
> in CI, but no AWS account is attached to a public interview repository. Treat
> it as the deployment I would propose, reviewable as code, rather than as a
> running environment.

## What it creates

| Resource | Notes |
|---|---|
| ECR repository | Immutable tags, scan on push, 30-image lifecycle |
| RDS PostgreSQL 17 | Multi-AZ, encrypted, 30-day backups, deletion protection |
| SQS queue + DLQ | 14-day retention, redrive after 5 failed receives |
| ECS cluster | Fargate, ARM64, Container Insights |
| API service | ≥2 tasks behind an internal ALB, circuit breaker with rollback |
| Relay service | 2 tasks — `SKIP LOCKED` means they partition, not duplicate |
| Migration task | Registered, never run as a service |
| Secrets Manager | The connection string, assembled once and never in a variable |

## What it deliberately does not create

**The VPC.** A service does not own the network it runs in, and a module that
creates one cannot be deployed twice into an account without surprises.
`vpc_id`, `private_subnet_ids`, and `public_subnet_ids` are inputs.

**The backend.** State configuration is environment-specific and belongs in a
config file passed at init time, not committed next to the resources it
manages. Committing a hard-coded backend is how one environment's state gets
overwritten by an apply meant for another.

## Usage

```bash
cp example.tfvars staging.tfvars   # then fill it in
terraform init -backend-config=envs/staging.backend.hcl
terraform plan  -var-file=staging.tfvars
terraform apply -var-file=staging.tfvars
```

`*.tfvars` is git-ignored apart from the example.

## Deploying a new version

Migrations run **before** the new revision is released, as a one-off task.
They are not in the service's startup path: rolling tasks would race the same
DDL, and a scale-up during an incident must not alter the schema.

```bash
# 1. build and push an immutable tag
docker build -f deploy/docker/Dockerfile --build-arg BINARY=transaction-api \
  --build-arg VERSION=v0.1.0 -t "$ECR_URL:v0.1.0" .
docker push "$ECR_URL:v0.1.0"

# 2. run migrations to completion, and check it succeeded
aws ecs run-task --cluster "$CLUSTER" \
  --task-definition "$MIGRATE_TASK_DEF" --launch-type FARGATE \
  --network-configuration "awsvpcConfiguration={subnets=[...],securityGroups=[...]}"

# 3. release
terraform apply -var-file=staging.tfvars -var image_tag=v0.1.0
```

`image_tag` rejects `latest` in a variable validation. A rollback needs a
reference that cannot move.

## Decisions worth knowing about

**The ALB is internal and its security group is not open to `0.0.0.0/0`.** The
endpoint is unauthenticated by contract
([ADR-0011](../../docs/adr/0011-authentication-off-by-default.md)), so network
position is currently the only access control there is. Producers are known
systems; reaching this listener should require being inside the VPC. Widening
that should be a deliberate decision, not an inherited default.

**Execution and task roles are separate.** The execution role pulls the image
and fetches the secret; the task role is what the running process holds. The
service therefore cannot read its own secrets at runtime — it receives the
connection string injected and has no reason, or permission, to fetch anything
else.

**The ALB health check targets `/healthz`, not `/readyz`.** ECS uses it to
decide whether to *replace* a task, and a brief database outage must not cause
a replacement storm. Readiness is what drains an instance; liveness is what
restarts it. They are different questions and this is where conflating them
costs the most.

**`deletion_protection` on both the database and the load balancer, plus a
final snapshot.** A `terraform destroy` against the wrong workspace must not be
able to quietly drop billing records.

**Tasks are ARM64.** Graviton is cheaper for the same throughput, and the
binary is static Go with nothing architecture-specific.

## What is missing before this is production-ready

Named rather than pretended:

- **Autoscaling.** Fixed task counts. Ingest is bursty by nature; target
  tracking on CPU and request count is the obvious next step.
- **Alarms.** The metrics exist and [the runbook](../../docs/runbook.md) says
  what to do about them, but no CloudWatch alarms are defined. The first three
  would be outbox lag, DLQ depth, and 5xx rate.
- **VPC endpoints** for ECR, Secrets Manager, CloudWatch, and SQS, so task
  egress can stop being `0.0.0.0/0`.
- **WAF** on the ALB, once anything faces a wider network.
- **A read replica**, when reporting queries start competing with ingest.
