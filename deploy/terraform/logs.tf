# One log group per component, so retention and access can differ later and a
# noisy component cannot bury a quiet one.
#
# Ingest logs are PII-redacted at the source (see docs/security.md), which is
# what makes a 30-day default acceptable rather than a liability.
resource "aws_cloudwatch_log_group" "api" {
  name              = "/ecs/${local.name}/api"
  retention_in_days = var.log_retention_days
}

resource "aws_cloudwatch_log_group" "relay" {
  name              = "/ecs/${local.name}/relay"
  retention_in_days = var.log_retention_days
}

resource "aws_cloudwatch_log_group" "migrate" {
  name              = "/ecs/${local.name}/migrate"
  retention_in_days = var.log_retention_days
}
