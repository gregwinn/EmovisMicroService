#!/usr/bin/env bash
#
# Formats a Go file immediately after it is edited.
#
# Without this, an agent's formatting drift is only discovered by `make lint`
# several steps later, and the fix lands as unrelated noise in the diff.
# Formatting at the point of edit keeps every diff about the change itself.

set -euo pipefail

# The hook receives the tool invocation as JSON on stdin.
file=$(jq -r '.tool_input.file_path // empty' 2>/dev/null || true)

[[ -n "$file" && "$file" == *.go && -f "$file" ]] || exit 0

gofmt -w "$file"
