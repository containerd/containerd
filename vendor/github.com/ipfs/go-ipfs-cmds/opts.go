package cmds

import ()

// Flag names
const (
	EncShort     = "enc"
	EncLong      = "encoding"
	RecShort     = "r"
	RecLong      = "recursive"
	ChanOpt      = "stream-channels"
	TimeoutOpt   = "timeout"
	OptShortHelp = "h"
	OptLongHelp  = "help"
	DerefLong    = "dereference-args"
	StdinName    = "stdin-name"
	Hidden       = "hidden"
	HiddenShort  = "H"
	Ignore       = "ignore"
	IgnoreRules  = "ignore-rules-path"
)

// options that are used by this package
var OptionEncodingType = StringOption(EncLong, EncShort, "The encoding type the output should be encoded with (json, xml, or text)").WithDefault("text")
var OptionRecursivePath = BoolOption(RecLong, RecShort, "Add directory paths recursively")
var OptionStreamChannels = BoolOption(ChanOpt, "Stream channel output")
var OptionTimeout = StringOption(TimeoutOpt, "Set a global timeout on the command")
var OptionDerefArgs = BoolOption(DerefLong, "Symlinks supplied in arguments are dereferenced")
var OptionStdinName = StringOption(StdinName, "Assign a name if the file source is stdin.")
var OptionHidden = BoolOption(Hidden, HiddenShort, "Include files that are hidden. Only takes effect on recursive add.")
var OptionIgnore = StringsOption(Ignore, "A rule (.gitignore-stype) defining which file(s) should be ignored (variadic, experimental)")
var OptionIgnoreRules = StringOption(IgnoreRules, "A path to a file with .gitignore-style ignore rules (experimental)")
