package cmds

// HelpText is a set of strings used to generate command help text. The help
// text follows formats similar to man pages, but not exactly the same.
type HelpText struct {
	// required
	Tagline               string            // used in <cmd usage>
	ShortDescription      string            // used in DESCRIPTION
	SynopsisOptionsValues map[string]string // mappings for synopsis generator

	// optional - whole section overrides
	Usage           string // overrides USAGE section
	LongDescription string // overrides DESCRIPTION section
	Options         string // overrides OPTIONS section
	Arguments       string // overrides ARGUMENTS section
	Subcommands     string // overrides SUBCOMMANDS section
	Synopsis        string // overrides SYNOPSIS field
}
