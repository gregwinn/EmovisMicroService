output "ecr_repository_url" {
  description = "Push images here; tag them immutably."
  value       = aws_ecr_repository.service.repository_url
}

output "api_endpoint" {
  description = "Internal load balancer hostname for the ingest API."
  value       = aws_lb.main.dns_name
}

output "cluster_name" {
  description = "ECS cluster, needed to run the migration task."
  value       = aws_ecs_cluster.main.name
}

output "migrate_task_definition" {
  description = "Run this to completion before releasing a new revision."
  value       = aws_ecs_task_definition.migrate.arn
}

output "transactions_queue_url" {
  description = "Queue the resolution pipeline consumes from."
  value       = aws_sqs_queue.transactions.url
}

output "transactions_dlq_url" {
  description = "Dead letter queue. Depth here means revenue is not being attributed — alert on it."
  value       = aws_sqs_queue.transactions_dlq.url
}

output "database_secret_arn" {
  description = "Secrets Manager ARN holding the connection string. The value never appears in Terraform output."
  value       = aws_secretsmanager_secret.database_url.arn
}
