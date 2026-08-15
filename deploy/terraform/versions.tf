terraform {
  required_version = ">= 1.9"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
  }

  # State is deliberately not configured here. Backend configuration is
  # environment-specific and belongs in a backend config file passed at init
  # time, not committed alongside the resources it manages:
  #
  #   terraform init -backend-config=envs/staging.backend.hcl
  #
  # Committing a hard-coded backend is how one environment's state gets
  # overwritten by an apply intended for another.
}

provider "aws" {
  region = var.region

  default_tags {
    tags = local.tags
  }
}
