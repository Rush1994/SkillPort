// Package skill defines the versioned artifact contract.
package skill

const (
	SchemaVersion   = 1
	ArtifactType    = "application/vnd.skillport.skill.v1"
	ConfigMediaType = "application/vnd.skillport.config.v1+json"
	LayerMediaType  = "application/vnd.skillport.content.v1.tar+gzip"
	// Read-only compatibility for artifacts published before the project rename.
	LegacyArtifactType    = "application/vnd.skillctl.skill.v1"
	LegacyConfigMediaType = "application/vnd.skillctl.config.v1+json"
	LegacyLayerMediaType  = "application/vnd.skillctl.content.v1.tar+gzip"
	StateDirectory        = ".skillport"
	LegacyStateDirectory  = ".skillctl"
)

func SupportedFormat(artifact, config, layer string) bool {
	return (artifact == ArtifactType && config == ConfigMediaType && layer == LayerMediaType) ||
		(artifact == LegacyArtifactType && config == LegacyConfigMediaType && layer == LegacyLayerMediaType)
}

func IsStateDirectory(name string) bool {
	return name == StateDirectory || name == LegacyStateDirectory
}

// Config is the JSON config blob referenced by the OCI manifest.
// Frontmatter remains in SKILL.md so unknown YAML fields survive round trips.
type Config struct {
	SchemaVersion int    `json:"schemaVersion"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Version       string `json:"version,omitempty"`
}
