resource "aws_ecs_cluster" "main" {
  name = local.name

  setting {
    name  = "containerInsights"
    value = "enabled"
  }
}

resource "aws_ecs_cluster_capacity_providers" "main" {
  cluster_name       = aws_ecs_cluster.main.name
  capacity_providers = ["FARGATE"]

  default_capacity_provider_strategy {
    capacity_provider = "FARGATE"
    weight            = 1
  }
}

locals {
  image = "${aws_ecr_repository.service.repository_url}:${var.image_tag}"

  log_configuration = {
    api = {
      logDriver = "awslogs"
      options = {
        "awslogs-group"         = aws_cloudwatch_log_group.api.name
        "awslogs-region"        = var.region
        "awslogs-stream-prefix" = "api"
      }
    }
    relay = {
      logDriver = "awslogs"
      options = {
        "awslogs-group"         = aws_cloudwatch_log_group.relay.name
        "awslogs-region"        = var.region
        "awslogs-stream-prefix" = "relay"
      }
    }
    migrate = {
      logDriver = "awslogs"
      options = {
        "awslogs-group"         = aws_cloudwatch_log_group.migrate.name
        "awslogs-region"        = var.region
        "awslogs-stream-prefix" = "migrate"
      }
    }
  }
}

# ---------------------------------------------------------------------------
# API
# ---------------------------------------------------------------------------

resource "aws_ecs_task_definition" "api" {
  family                   = "${local.name}-api"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = var.api_cpu
  memory                   = var.api_memory
  execution_role_arn       = aws_iam_role.execution.arn
  task_role_arn            = aws_iam_role.task.arn

  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = "ARM64"
  }

  container_definitions = jsonencode([{
    name      = "api"
    image     = local.image
    essential = true

    portMappings = [{ containerPort = 8080, protocol = "tcp" }]

    environment = concat(local.common_environment, [
      { name = "HTTP_ADDR", value = ":8080" },
      { name = "SERVICE_NAME", value = "transaction-ingest" },
    ])
    secrets = local.common_secrets

    # The image is distroless with no shell, so the binary probes itself.
    healthCheck = {
      command     = ["CMD", "/app", "-healthcheck"]
      interval    = 15
      timeout     = 5
      retries     = 3
      startPeriod = 10
    }

    logConfiguration = local.log_configuration.api

    readonlyRootFilesystem = true
    linuxParameters        = { initProcessEnabled = true }
  }])
}

resource "aws_ecs_service" "api" {
  name            = "${local.name}-api"
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.api.arn
  desired_count   = var.api_desired_count
  launch_type     = "FARGATE"

  network_configuration {
    subnets         = var.private_subnet_ids
    security_groups = [aws_security_group.tasks.id]
    # Private subnets with a NAT gateway. A service handling movement data has
    # no business holding a public IP.
    assign_public_ip = false
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.api.arn
    container_name   = "api"
    container_port   = 8080
  }

  # Never take capacity below the desired count during a deploy, and allow one
  # extra task so the rollout is genuinely rolling.
  deployment_minimum_healthy_percent = 100
  deployment_maximum_percent         = 200

  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }

  # Long enough for the task to start, run its checks, and register — but not
  # so long that a broken deploy sits unhealthy before the circuit breaker acts.
  health_check_grace_period_seconds = 30

  enable_execute_command = false

  depends_on = [aws_lb_listener.https]
}

# ---------------------------------------------------------------------------
# Outbox relay
# ---------------------------------------------------------------------------

resource "aws_ecs_task_definition" "relay" {
  family                   = "${local.name}-relay"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = var.relay_cpu
  memory                   = var.relay_memory
  execution_role_arn       = aws_iam_role.execution.arn
  task_role_arn            = aws_iam_role.task.arn

  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = "ARM64"
  }

  container_definitions = jsonencode([{
    name      = "relay"
    image     = local.image
    essential = true
    command   = ["/app"]

    environment = concat(local.common_environment, [
      { name = "SERVICE_NAME", value = "transaction-ingest" },
      { name = "METRICS_ADDR", value = ":9090" },
      { name = "SQS_QUEUE_URL", value = aws_sqs_queue.transactions.url },
    ])
    secrets = local.common_secrets

    logConfiguration       = local.log_configuration.relay
    readonlyRootFilesystem = true
  }])
}

resource "aws_ecs_service" "relay" {
  name            = "${local.name}-relay"
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.relay.arn
  # Two replicas: FOR UPDATE SKIP LOCKED means they partition the backlog
  # rather than duplicate or serialize on it, so this is throughput and
  # availability rather than a hot standby.
  desired_count = 2
  launch_type   = "FARGATE"

  network_configuration {
    subnets          = var.private_subnet_ids
    security_groups  = [aws_security_group.tasks.id]
    assign_public_ip = false
  }

  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }
}

# ---------------------------------------------------------------------------
# Migrations
# ---------------------------------------------------------------------------

# Registered as a task definition but never run as a service. Deployment runs
# it once, to completion, before releasing a new revision:
#
#   aws ecs run-task --cluster <cluster> --task-definition <this> --launch-type FARGATE
#
# Migrations are not in the service's startup path on purpose: rolling tasks
# would race the same DDL, and a scale-up during an incident must not be able
# to alter the schema. See docs/adr/0010-postgres-without-an-orm.md.
resource "aws_ecs_task_definition" "migrate" {
  family                   = "${local.name}-migrate"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = 256
  memory                   = 512
  execution_role_arn       = aws_iam_role.execution.arn
  task_role_arn            = aws_iam_role.task.arn

  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = "ARM64"
  }

  container_definitions = jsonencode([{
    name      = "migrate"
    image     = local.image
    essential = true
    command   = ["up"]

    environment            = local.common_environment
    secrets                = local.common_secrets
    logConfiguration       = local.log_configuration.migrate
    readonlyRootFilesystem = true
  }])
}
