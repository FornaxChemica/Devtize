# Compatibility Notes

## Phase D.1

Phase D.1 adds `dvz sync git`, additive registry status in `find` and `doctor`
JSON, additive sync provenance on command knowledge, and schema-version-1
content-addressed provider snapshots. Existing builtin command JSON fields,
configuration schema version 1, history schema version 1, and push-only ship
JSON remain compatible.

The registry cache is derived and may be deleted without migration. Existing
history readers accept global `registry.sync` records because they use the
same additive schema-version-1 record shape. Synced knowledge is discovery-only
and does not add or alter executable capability IDs.
