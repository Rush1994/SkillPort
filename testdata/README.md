# Test fixtures

All content is inert data and must never be executed. Under `skills/`,
`valid/hello-skill` is a metadata and directory round-trip input.
`invalid/missing-name` and `invalid/duplicate-key` must fail validation.
No fixture contains real credentials. These inputs are now exercised by the
metadata validator and the archive round-trip tests. The separate credential
helper fixture under internal/auth/testdata is test infrastructure, never Skill content.
