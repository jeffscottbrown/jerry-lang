// Package runtime embeds the Jerry C runtime into the compiler binary.
// The C files live in src/ to avoid being misinterpreted as CGo sources.
package runtime

import "embed"

//go:embed src/runtime.c src/runtime.h
var Files embed.FS
