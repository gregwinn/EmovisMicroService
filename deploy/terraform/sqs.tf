# Where the outbox relay publishes. The resolution pipeline consumes from here.
resource "aws_sqs_queue" "transactions" {
  name = "${local.name}-transactions"

  # Generous: the relay retries with backoff and the resolution pipeline may be
  # slow. Losing an event to expiry would defeat the outbox entirely.
  message_retention_seconds  = 1209600 # 14 days, the maximum
  visibility_timeout_seconds = 60

  sqs_managed_sse_enabled = true

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.transactions_dlq.arn
    maxReceiveCount     = 5
  })
}

# A consumer that cannot process a message must not spin on it forever, and the
# message must not be discarded. Depth here should be alerted on: it means
# revenue is not being attributed.
resource "aws_sqs_queue" "transactions_dlq" {
  name = "${local.name}-transactions-dlq"

  message_retention_seconds = 1209600
  sqs_managed_sse_enabled   = true
}

resource "aws_sqs_queue_redrive_allow_policy" "transactions_dlq" {
  queue_url = aws_sqs_queue.transactions_dlq.id

  redrive_allow_policy = jsonencode({
    redrivePermission = "byQueue"
    sourceQueueArns   = [aws_sqs_queue.transactions.arn]
  })
}
