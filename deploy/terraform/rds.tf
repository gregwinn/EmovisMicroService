resource "aws_db_subnet_group" "main" {
  name       = local.name
  subnet_ids = var.private_subnet_ids
}

# The database password is generated here and never leaves AWS. It is not a
# variable, so it cannot end up in a tfvars file, a CI log, or a shell history.
resource "random_password" "database" {
  length = 32
  # RDS rejects several punctuation characters in master passwords, and a
  # connection string is easier to reason about without them.
  special = false
}

resource "aws_db_instance" "main" {
  identifier = local.name

  engine         = "postgres"
  engine_version = "17"
  instance_class = var.db_instance_class

  allocated_storage     = var.db_allocated_storage
  max_allocated_storage = var.db_allocated_storage * 5
  storage_type          = "gp3"
  storage_encrypted     = true

  db_name  = "ingest"
  username = "ingest"
  password = random_password.database.result

  db_subnet_group_name   = aws_db_subnet_group.main.name
  vpc_security_group_ids = [aws_security_group.database.id]
  publicly_accessible    = false

  # This table is a movement database. A lost day of it is lost revenue and a
  # compliance problem, so backups are not optional and neither is multi-AZ.
  multi_az                = true
  backup_retention_period = 30
  backup_window           = "03:00-04:00"
  maintenance_window      = "sun:04:00-sun:05:00"

  # Delete protection on, and a final snapshot: a `terraform destroy` against
  # the wrong workspace must not be able to silently drop billing records.
  deletion_protection       = true
  skip_final_snapshot       = false
  final_snapshot_identifier = "${local.name}-final"

  performance_insights_enabled    = true
  enabled_cloudwatch_logs_exports = ["postgresql"]

  auto_minor_version_upgrade = true
  apply_immediately          = false
}

# The connection string is assembled once and injected as a secret, so no task
# definition contains a credential and nothing has to reassemble it correctly.
resource "aws_secretsmanager_secret" "database_url" {
  name        = "${local.name}/database-url"
  description = "PostgreSQL connection string for the transaction ingest service"

  recovery_window_in_days = 7
}

resource "aws_secretsmanager_secret_version" "database_url" {
  secret_id = aws_secretsmanager_secret.database_url.id

  secret_string = format(
    "postgres://%s:%s@%s/%s?sslmode=require",
    aws_db_instance.main.username,
    random_password.database.result,
    aws_db_instance.main.endpoint,
    aws_db_instance.main.db_name,
  )
}
