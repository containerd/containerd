package securityprofile

// Config captures user-configurable delivery options shared by security profile services.
type Config struct {
	LabelPrefix string `toml:"label_prefix" json:"labelPrefix"`
	TargetDir   string `toml:"target_dir" json:"targetDir"`
}

// Normalize returns a copy of cfg merged with defaults, substituting any empty fields with the provided defaults.
func Normalize(cfg *Config, defaults Config) Config {
	result := defaults
	if cfg == nil {
		return result
	}
	if cfg.LabelPrefix != "" {
		result.LabelPrefix = cfg.LabelPrefix
	}
	if cfg.TargetDir != "" {
		result.TargetDir = cfg.TargetDir
	}
	return result
}
