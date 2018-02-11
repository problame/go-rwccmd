package rwccmd

import (
	"context"
)

type contextKey int

const (
	contextKeyLog contextKey = iota
)

// The interface a logger must implement for ContextWithLog
type Logger interface {
	Printf(format string, args ...interface{})
}

type discardLog struct {}
func (d discardLog) Printf(format string, args ...interface{}) {}

func contextLog(ctx context.Context) (log Logger) {
	log, ok := ctx.Value(contextKeyLog).(Logger)
	if !ok {
		log = discardLog{}
	}
	return log
}

// ContextWithLog adds a Logger log to ctx that will be called by rwccmd.Cmd.
// This is most useful for debugging.
func ContextWithLog(ctx context.Context, log Logger) context.Context {
	return context.WithValue(ctx, contextKeyLog, log)
}