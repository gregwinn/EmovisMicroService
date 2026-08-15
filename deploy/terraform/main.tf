# Transaction ingest service on ECS Fargate.
#
# Scope: this deploys the service, its database, and its queue into an existing
# VPC. It does not create the network — a service does not own the VPC it runs
# in, and a module that creates one cannot be deployed twice into the same
# account without surprises.
#
# Nothing here has been applied. It is written, formatted, and validated in CI,
# but no AWS account is attached to a public interview repository. See
# deploy/terraform/README.md.

locals {
  name = "transaction-ingest-${var.environment}"

  tags = {
    Service     = "transaction-ingest"
    Environment = var.environment
    ManagedBy   = "terraform"
    Repository  = "github.com/gregwinn/EmovisMicroService"
  }

  # Configuration shared by both task definitions. Kept in one place so the API
  # and the relay cannot drift into disagreeing about the database or the
  # ingest policy they enforce.
  common_environment = [
    { name = "ENVIRONMENT", value = var.environment },
    { name = "LOG_LEVEL", value = var.log_level },
    { name = "LOG_FORMAT", value = "json" },
    { name = "TRANSACTION_TYPES", value = join(",", var.transaction_types) },
    { name = "DEFAULT_CURRENCY", value = var.default_currency },
  ]

  common_secrets = [
    { name = "DATABASE_URL", valueFrom = aws_secretsmanager_secret.database_url.arn },
  ]
}

data "aws_caller_identity" "current" {}
