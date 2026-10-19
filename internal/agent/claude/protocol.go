package claude

// ProtocolObserver receives every raw JSON line as it flows over stdio,
// before any parsing. Implementations must be fast; avoid operations that
// can block for non-trivial time (network calls, contested locks). A log
// file write is acceptable. msg is the raw JSON without the trailing newline.
//
// Methods are called from several goroutines at once, so implementations
// must be safe for concurrent use. The order of OnSend calls between
// concurrent writers may differ from the order on the wire.
type ProtocolObserver interface {
	OnSend(msg string)
	OnReceive(msg string)
}

// noopObserver is the default ProtocolObserver when none is provided.
type noopObserver struct{}

func (noopObserver) OnSend(string)    {}
func (noopObserver) OnReceive(string) {}
