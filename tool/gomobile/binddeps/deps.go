// Package binddeps keeps the local Bridra binding package in the isolated
// gomobile module graph. gomobile requires both its own tools and the binding
// target to be dependencies of the module that executes bind.
package binddeps

import _ "github.com/cluion/bridra/backend/mobilebridge"
