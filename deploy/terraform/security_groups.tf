# The load balancer is the only thing reachable from outside.
resource "aws_security_group" "alb" {
  name        = "${local.name}-alb"
  description = "Ingress to the transaction ingest load balancer"
  vpc_id      = var.vpc_id

  lifecycle {
    create_before_destroy = true
  }
}

# Deliberately not open to 0.0.0.0/0.
#
# The endpoint is unauthenticated by contract (ADR-0011), so network position is
# currently the only access control there is. Producers are known systems, so
# reaching this listener should require being inside the VPC — via a private
# link, a VPN, or a peered network. Widening this is a decision that should be
# made explicitly, not inherited from a default.
resource "aws_vpc_security_group_ingress_rule" "alb_https_from_vpc" {
  security_group_id = aws_security_group.alb.id
  description       = "HTTPS from within the VPC"

  cidr_ipv4   = data.aws_vpc.selected.cidr_block
  from_port   = 443
  to_port     = 443
  ip_protocol = "tcp"
}

resource "aws_vpc_security_group_egress_rule" "alb_to_tasks" {
  security_group_id = aws_security_group.alb.id
  description       = "Forward to the API tasks"

  referenced_security_group_id = aws_security_group.tasks.id
  from_port                    = 8080
  to_port                      = 8080
  ip_protocol                  = "tcp"
}

data "aws_vpc" "selected" {
  id = var.vpc_id
}

# Tasks accept traffic only from the load balancer.
resource "aws_security_group" "tasks" {
  name        = "${local.name}-tasks"
  description = "Transaction ingest ECS tasks"
  vpc_id      = var.vpc_id

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_vpc_security_group_ingress_rule" "tasks_from_alb" {
  security_group_id = aws_security_group.tasks.id
  description       = "API traffic from the load balancer"

  referenced_security_group_id = aws_security_group.alb.id
  from_port                    = 8080
  to_port                      = 8080
  ip_protocol                  = "tcp"
}

# Outbound is unrestricted because tasks must reach ECR, CloudWatch, Secrets
# Manager, and SQS. Tightening this means VPC endpoints for each, which is the
# right answer at scale and overkill here — noted rather than pretended.
resource "aws_vpc_security_group_egress_rule" "tasks_egress" {
  security_group_id = aws_security_group.tasks.id
  description       = "Outbound to AWS services"

  cidr_ipv4   = "0.0.0.0/0"
  ip_protocol = "-1"
}

# The database accepts connections only from the tasks.
resource "aws_security_group" "database" {
  name        = "${local.name}-database"
  description = "Transaction ingest database"
  vpc_id      = var.vpc_id

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_vpc_security_group_ingress_rule" "database_from_tasks" {
  security_group_id = aws_security_group.database.id
  description       = "PostgreSQL from the ECS tasks"

  referenced_security_group_id = aws_security_group.tasks.id
  from_port                    = 5432
  to_port                      = 5432
  ip_protocol                  = "tcp"
}
