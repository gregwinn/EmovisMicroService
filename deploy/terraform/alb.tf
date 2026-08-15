resource "aws_lb" "main" {
  name               = local.name
  load_balancer_type = "application"
  internal           = true
  subnets            = var.public_subnet_ids
  security_groups    = [aws_security_group.alb.id]

  drop_invalid_header_fields = true
  enable_deletion_protection = true
}

resource "aws_lb_target_group" "api" {
  name        = "${local.name}-api"
  port        = 8080
  protocol    = "HTTP"
  target_type = "ip"
  vpc_id      = var.vpc_id

  # /healthz, not /readyz. The load balancer decides whether to send traffic —
  # and a task whose database is briefly unreachable should stop receiving
  # requests, which is what readiness is for. But ECS also uses this to decide
  # whether to replace a task, and a database blip must not trigger a
  # replacement storm. Draining is handled by the readiness probe reporting 503
  # from the application, which returns 500s that surface in metrics.
  health_check {
    path                = "/healthz"
    matcher             = "200"
    interval            = 10
    timeout             = 5
    healthy_threshold   = 2
    unhealthy_threshold = 3
  }

  # Enough to finish in-flight requests plus the service's own 15s shutdown
  # grace, so a rolling deploy does not drop a transaction mid-write.
  deregistration_delay = 30

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_lb_listener" "https" {
  load_balancer_arn = aws_lb.main.arn
  port              = 443
  protocol          = "HTTPS"
  ssl_policy        = "ELBSecurityPolicy-TLS13-1-2-2021-06"
  certificate_arn   = var.certificate_arn

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.api.arn
  }
}
