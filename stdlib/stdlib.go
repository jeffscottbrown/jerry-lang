// Package stdlib embeds the Jerry standard library source files into the
// compiler binary so the compiler is fully self-contained.
package stdlib

import "embed"

// Files is an fs.FS containing all *.jer files in the stdlib directory.
// It is used by the compiler when no local override is provided.
//
//go:embed *.jer
var Files embed.FS
