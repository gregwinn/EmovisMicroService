# Copy to <environment>.tfvars and fill in. Do not commit a real one — the
# .gitignore excludes *.tfvars for that reason.

region      = "us-east-1"
environment = "staging"

vpc_id             = "vpc-0123456789abcdef0"
private_subnet_ids = ["subnet-0aaa...", "subnet-0bbb..."]
public_subnet_ids  = ["subnet-0ccc...", "subnet-0ddd..."]

certificate_arn = "arn:aws:acm:us-east-1:123456789012:certificate/..."

# Immutable. Never "latest" — a rollback needs a reference that cannot move.
image_tag = "v0.1.0"

transaction_types = ["toll", "violation", "fee"]
default_currency  = "USD"
