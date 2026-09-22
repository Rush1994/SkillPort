# SkillPort OCI format v1 (draft)

This is the current development format, not a compatibility claim for
acr-skill or any Agent platform. Media types are project-specific identifiers,
not registered vendor ownership. A future incompatible format gets a new version.

## Manifest and config

OCI image manifest `application/vnd.oci.image.manifest.v1+json`, schemaVersion 2:

- artifactType: `application/vnd.skillport.skill.v1`
- config: one digest/size descriptor with mediaType `application/vnd.skillport.config.v1+json`
- layers: exactly one digest/size descriptor with mediaType `application/vnd.skillport.content.v1.tar+gzip`

Config JSON uses schemaVersion 1, name, description and optional version, as
defined in `internal/skill/format.go`. SKILL.md is authoritative; config fields
must agree with its frontmatter. Unknown frontmatter fields remain in SKILL.md.
All descriptors use SHA-256 and are checked against actual downloaded bytes.
Readers reject unsupported versions, media types and extra layers.

Readers also accept the complete pre-rename `application/vnd.skillctl.*` v1
artifact/config/layer triplet. Mixed old/new triplets are rejected. Writers emit
only `application/vnd.skillport.*`, so a renamed artifact has a different manifest
digest even when its content is unchanged; publish it under a new tag.

## Content layer

One gzip-compressed POSIX tar stream; paths are relative to the Skill root,
without a containing directory. Root SKILL.md is mandatory. File bytes are
preserved. File paths are sorted lexically by UTF-8 bytes; directory entries
are omitted, with parents recreated during extraction. Empty directories are
not part of v1. Use fixed gzip header (zero time, no name/comment, OS 255),
fixed compression level, tar PAX format, zero UID/GID/time and empty user/group.
Regular-file mode is 0644, or 0755 for executable files. Windows preserves
downloaded executable metadata in the synchronization record; new Windows files
default to 0644. No wall-clock publication timestamp enters hashed content.
Packer version and dependency changes require digest regression testing.

Only regular files are permitted. Reject symlinks, hardlinks, devices, absolute
paths, traversal, duplicate/case-colliding paths, and Windows-incompatible names.
Enforce byte/entry/depth limits on actual streaming data, not only headers.
`.skillport`, excluded content, temporary state and backups never enter the layer.
See [the usage guide](usage.md) for validation rules and current fixed limits.

## Local state and publication

`.skillport` is local-only; schemaVersion 1 state records source host,
repository, tag (if any), manifest digest, sync time, file hashes/modes, and
baseline bytes. Its paths require validation independently of remote archives.
Readers fall back to the pre-rename `.skillctl/state.json` only when the new
record is absent. Both state directories are excluded from publication.
Downloads stage, verify and validate before committing contents and state.
Manifest commit determines publication success; local-state failure after that
must report the published digest. Client tag checks are not atomic concurrency
control. Unique version tags plus Harbor immutability are required for strict
protection against concurrent overwrites.
