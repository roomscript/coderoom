package interpreter

import "github.com/roomscript/coderoom/internal/interpreter/monolith"

// SessionController is the existing session dependency port. One fallback runtime
// consumes it during migration; the facade adds no independent Session observer.
type SessionController = monolith.SessionController
