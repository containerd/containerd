package cmds

type Flusher interface {
	Flush() error
}

type flushfwder struct {
	ResponseEmitter
	Flusher
}

func (ff *flushfwder) Close() error {
	err := ff.Flush()
	if err != nil {
		return err
	}

	return ff.ResponseEmitter.Close()
}

func NewFlushForwarder(re ResponseEmitter, f Flusher) ResponseEmitter {
	return &flushfwder{ResponseEmitter: re, Flusher: f}
}
