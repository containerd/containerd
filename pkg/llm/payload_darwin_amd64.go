package llm

import (
	"embed"
)

//go:embed llama.cpp/ggml-metal.metal llama.cpp/build/darwin/x86_64/*/lib/*.dylib*
var libEmbed embed.FS
