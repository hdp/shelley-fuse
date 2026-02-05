---
id: sf-94sv
status: closed
deps: []
links: [sf-vrsz]
created: 2026-02-04T13:33:17Z
type: bug
priority: 2
---
# model= display names for custom models not supported

The 'model=' configuration parameter in /conversation/{id}/ctl currently requires the internal model ID (e.g., 'custom-f999b9b0') instead of the display name (e.g., 'kimi-2.5-fireworks'). Similarly, custom models don't appear as directories in /models using their display names.

## Design

1. Map from display name to internal ID for custom models\n2. Use display names as directory names in /models\n3. When parsing 'model=' in ctl, look up by display name and use internal ID for API calls

## Acceptance Criteria

1. Can set 'model=kimi-2.5-fireworks' in ctl file\n2. /models/kimi-2.5-fireworks/ exists as a directory with id file containing internal ID\n3. Custom model directories show up after listing /models\n4. Existing configs with internal IDs continue to work (backward compatibility)


## Notes

**2026-02-04T13:34:19Z**

Superseded by sf-vrsz - that ticket has the correct acceptance criteria (no backward compatibility, custom-XXX IDs never exposed)
