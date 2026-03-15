#!/bin/bash
set -euo pipefail

# Build the gravix send event command from action inputs.
CMD=(gravix send event)
CMD+=(--service "$INPUT_SERVICE")
CMD+=(--type "$INPUT_EVENT_TYPE")

if [ -n "${INPUT_MESSAGE:-}" ]; then
  CMD+=(--message "$INPUT_MESSAGE")
fi

if [ -n "${INPUT_ENTITY_ID:-}" ]; then
  CMD+=(--entity-id "$INPUT_ENTITY_ID")
fi

# Parse multi-line properties (one key=value per line).
if [ -n "${INPUT_PROPERTIES:-}" ]; then
  while IFS= read -r line; do
    line="$(echo "$line" | xargs)"  # trim whitespace
    if [ -n "$line" ]; then
      CMD+=(--prop "$line")
    fi
  done <<< "$INPUT_PROPERTIES"
fi

echo "::group::Gravix Deploy Event"
echo "Endpoint: ${GRAVIX_ENDPOINT}"
echo "Service:  ${INPUT_SERVICE}"
echo "Type:     ${INPUT_EVENT_TYPE}"
if [ -n "${INPUT_MESSAGE:-}" ]; then
  echo "Message:  ${INPUT_MESSAGE}"
fi
echo "::endgroup::"

# Execute the command.
"${CMD[@]}"
