package events

import (
	"context"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/namespaces"
	"github.com/sirupsen/logrus"
)

var (
	G = GetPoster
)

// Poster posts the event.
type Poster interface {
	Post(ctx context.Context, evt Event) error
}

type posterKey struct{}

func WithPoster(ctx context.Context, poster Poster) context.Context {
	return context.WithValue(ctx, posterKey{}, poster)
}

func GetPoster(ctx context.Context) Poster {
	poster := ctx.Value(posterKey{})

	if poster == nil {
		tx, _ := getTx(ctx)
		topic := getTopic(ctx)

		// likely means we don't have a configured event system. Just return
		// the default poster, which merely logs events.
		return posterFunc(func(ctx context.Context, evt Event) error {
			fields := logrus.Fields{"event": evt}

			if topic != "" {
				fields["topic"] = topic
			}
			ns, _ := namespaces.Namespace(ctx)
			fields["ns"] = ns

			if tx != nil {
				fields["tx.id"] = tx.id
				if tx.parent != nil {
					fields["tx.parent.id"] = tx.parent.id
				}
			}

			log.G(ctx).WithFields(fields).Debug("event fired")

			return nil
		})
	}

	return poster.(Poster)
}

type posterFunc func(ctx context.Context, evt Event) error

func (fn posterFunc) Post(ctx context.Context, evt Event) error {
	fn(ctx, evt)
	return nil
}
