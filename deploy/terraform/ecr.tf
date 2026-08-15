# One repository for all three binaries. They are built from one Dockerfile and
# share a tag per release, so keeping them together means a rollback moves the
# whole service to a consistent point rather than three independently-versioned
# pieces.
resource "aws_ecr_repository" "service" {
  name = local.name

  # Immutable tags: a deployed tag can never be repointed at different bytes.
  # Without this, "roll back to v1.2.3" is not a guarantee.
  image_tag_mutability = "IMMUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }

  encryption_configuration {
    encryption_type = "AES256"
  }
}

resource "aws_ecr_lifecycle_policy" "service" {
  repository = aws_ecr_repository.service.name

  policy = jsonencode({
    rules = [{
      rulePriority = 1
      description  = "Keep the last 30 images; older ones are not rollback targets."
      selection = {
        tagStatus   = "any"
        countType   = "imageCountMoreThan"
        countNumber = 30
      }
      action = { type = "expire" }
    }]
  })
}
