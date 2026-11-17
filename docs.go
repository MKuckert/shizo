// Package shizo embeds the operator documentation (docs/*.md) so a bare
// release binary can serve it via `shizo docs` without any source checkout.
// It is the single file at the module root; the hook itself lives in src/.
package shizo

import "embed"

//go:embed docs/*.md
var Docs embed.FS
