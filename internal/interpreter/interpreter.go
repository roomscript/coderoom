package interpreter

import "context"

// New starts an Interpreter backed by sess.
func New(ctx context.Context, sess SessionController, cwd string, opts ...Option) *Interpreter {
	if ctx == nil {
		ctx = context.Background()
	}
	model := newInterpreterModel()
	i := &Interpreter{model: model}
	i.executor = newInterpreterExecutor(ctx, sess, cwd, model)
	for _, opt := range opts {
		opt(i)
	}
	i.executor.start()
	return i
}
