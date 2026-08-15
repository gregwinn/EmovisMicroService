variable "region" {
  description = "AWS region to deploy into."
  type        = string
}

variable "environment" {
  description = "Deployment environment name, used in resource names and tags."
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{1,15}$", var.environment))
    error_message = "environment must be lower-case alphanumeric with hyphens, 2-16 characters."
  }
}

variable "vpc_id" {
  description = "Existing VPC to deploy into. A service does not own its network."
  type        = string
}

variable "private_subnet_ids" {
  description = "Private subnets for the ECS tasks and the database. At least two, in different availability zones."
  type        = list(string)

  validation {
    condition     = length(var.private_subnet_ids) >= 2
    error_message = "at least two private subnets are required for multi-AZ."
  }
}

variable "public_subnet_ids" {
  description = "Public subnets for the load balancer."
  type        = list(string)

  validation {
    condition     = length(var.public_subnet_ids) >= 2
    error_message = "at least two public subnets are required for a load balancer."
  }
}

variable "image_tag" {
  description = "Container image tag to deploy. Never 'latest' — a rollback needs an immutable reference."
  type        = string

  validation {
    condition     = var.image_tag != "latest"
    error_message = "image_tag must be immutable; 'latest' cannot be rolled back to a known state."
  }
}

# ---------------------------------------------------------------------------
# Ingest policy. These mirror the service's environment configuration; see the
# table in the repository README.
# ---------------------------------------------------------------------------

variable "transaction_types" {
  description = "Accepted billable event types. Operator configuration, not a code change."
  type        = list(string)
  default     = ["toll", "violation", "fee"]
}

variable "default_currency" {
  description = "ISO-4217 currency applied when a producer omits one."
  type        = string
  default     = "USD"
}

variable "log_level" {
  description = "debug | info | warn | error"
  type        = string
  default     = "info"
}

# ---------------------------------------------------------------------------
# Capacity
# ---------------------------------------------------------------------------

variable "api_desired_count" {
  description = "Number of API tasks. At least two, so a deploy or an AZ loss is not an outage."
  type        = number
  default     = 2

  validation {
    condition     = var.api_desired_count >= 2
    error_message = "run at least two API tasks; one is a single point of failure."
  }
}

variable "api_cpu" {
  description = "CPU units for an API task (1024 = 1 vCPU)."
  type        = number
  default     = 512
}

variable "api_memory" {
  description = "Memory (MiB) for an API task."
  type        = number
  default     = 1024
}

variable "relay_cpu" {
  description = "CPU units for the outbox relay task."
  type        = number
  default     = 256
}

variable "relay_memory" {
  description = "Memory (MiB) for the outbox relay task."
  type        = number
  default     = 512
}

variable "db_instance_class" {
  description = "RDS instance class."
  type        = string
  default     = "db.t4g.micro"
}

variable "db_allocated_storage" {
  description = "RDS allocated storage in GiB."
  type        = number
  default     = 20
}

variable "log_retention_days" {
  description = "CloudWatch log retention. Note that ingest logs are PII-redacted; see docs/security.md."
  type        = number
  default     = 30
}

variable "certificate_arn" {
  description = "ACM certificate for the load balancer listener."
  type        = string
}
