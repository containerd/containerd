package log

import (
	"sync"

	"go.uber.org/multierr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var _ zapcore.Core = (*lockedMultiCore)(nil)

type lockedMultiCore struct {
	mu    sync.RWMutex // guards mutations to cores slice
	cores []zapcore.Core
}

func (l *lockedMultiCore) With(fields []zapcore.Field) zapcore.Core {
	l.mu.RLock()
	defer l.mu.RUnlock()
	sub := &lockedMultiCore{
		cores: make([]zapcore.Core, len(l.cores)),
	}
	for i := range l.cores {
		sub.cores[i] = l.cores[i].With(fields)
	}
	return sub
}

func (l *lockedMultiCore) Enabled(lvl zapcore.Level) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for i := range l.cores {
		if l.cores[i].Enabled(lvl) {
			return true
		}
	}
	return false
}

func (l *lockedMultiCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for i := range l.cores {
		ce = l.cores[i].Check(ent, ce)
	}
	return ce
}

func (l *lockedMultiCore) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var err error
	for i := range l.cores {
		err = multierr.Append(err, l.cores[i].Write(ent, fields))
	}
	return err
}

func (l *lockedMultiCore) Sync() error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var err error
	for i := range l.cores {
		err = multierr.Append(err, l.cores[i].Sync())
	}
	return err
}

func (l *lockedMultiCore) AddCore(core zapcore.Core) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.cores = append(l.cores, core)
}

func (l *lockedMultiCore) DeleteCore(core zapcore.Core) {
	l.mu.Lock()
	defer l.mu.Unlock()

	w := 0
	for i := 0; i < len(l.cores); i++ {
		if l.cores[i] == core {
			continue
		}
		l.cores[w] = l.cores[i]
		w++
	}
	l.cores = l.cores[:w]
}

func (l *lockedMultiCore) ReplaceCore(original, replacement zapcore.Core) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for i := 0; i < len(l.cores); i++ {
		if l.cores[i] == original {
			l.cores[i] = replacement
		}
	}
}

func newCore(format LogFormat, ws zapcore.WriteSyncer, level LogLevel) zapcore.Core {
	encCfg := zap.NewProductionEncoderConfig()
	encCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	var encoder zapcore.Encoder
	switch format {
	case PlaintextOutput:
		encCfg.EncodeLevel = zapcore.CapitalLevelEncoder
		encoder = zapcore.NewConsoleEncoder(encCfg)
	case JSONOutput:
		encoder = zapcore.NewJSONEncoder(encCfg)
	default:
		encCfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
		encoder = zapcore.NewConsoleEncoder(encCfg)
	}

	return zapcore.NewCore(encoder, ws, zap.NewAtomicLevelAt(zapcore.Level(level)))
}
