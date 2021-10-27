package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	iface "github.com/ipfs/interface-go-ipfs-core"
	caopts "github.com/ipfs/interface-go-ipfs-core/options"
	"github.com/libp2p/go-libp2p-core/peer"
)

type PubsubAPI HttpApi

func (api *PubsubAPI) Ls(ctx context.Context) ([]string, error) {
	var out struct {
		Strings []string
	}

	if err := api.core().Request("pubsub/ls").Exec(ctx, &out); err != nil {
		return nil, err
	}

	return out.Strings, nil
}

func (api *PubsubAPI) Peers(ctx context.Context, opts ...caopts.PubSubPeersOption) ([]peer.ID, error) {
	options, err := caopts.PubSubPeersOptions(opts...)
	if err != nil {
		return nil, err
	}

	var out struct {
		Strings []string
	}

	if err := api.core().Request("pubsub/peers", options.Topic).Exec(ctx, &out); err != nil {
		return nil, err
	}

	res := make([]peer.ID, len(out.Strings))
	for i, sid := range out.Strings {
		id, err := peer.Decode(sid)
		if err != nil {
			return nil, err
		}
		res[i] = id
	}
	return res, nil
}

func (api *PubsubAPI) Publish(ctx context.Context, topic string, message []byte) error {
	return api.core().Request("pubsub/pub", topic).
		FileBody(bytes.NewReader(message)).
		Exec(ctx, nil)
}

type pubsubSub struct {
	messages chan pubsubMessage

	done    chan struct{}
	rcloser func() error
}

type pubsubMessage struct {
	JFrom     []byte   `json:"from,omitempty"`
	JData     []byte   `json:"data,omitempty"`
	JSeqno    []byte   `json:"seqno,omitempty"`
	JTopicIDs []string `json:"topicIDs,omitempty"`

	from peer.ID
	err  error
}

func (msg *pubsubMessage) From() peer.ID {
	return msg.from
}

func (msg *pubsubMessage) Data() []byte {
	return msg.JData
}

func (msg *pubsubMessage) Seq() []byte {
	return msg.JSeqno
}

func (msg *pubsubMessage) Topics() []string {
	return msg.JTopicIDs
}

func (s *pubsubSub) Next(ctx context.Context) (iface.PubSubMessage, error) {
	select {
	case msg, ok := <-s.messages:
		if !ok {
			return nil, io.EOF
		}
		if msg.err != nil {
			return nil, msg.err
		}
		var err error
		msg.from, err = peer.IDFromBytes(msg.JFrom)
		return &msg, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (api *PubsubAPI) Subscribe(ctx context.Context, topic string, opts ...caopts.PubSubSubscribeOption) (iface.PubSubSubscription, error) {
	options, err := caopts.PubSubSubscribeOptions(opts...)
	if err != nil {
		return nil, err
	}

	resp, err := api.core().Request("pubsub/sub", topic).
		Option("discover", options.Discover).Send(ctx)

	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error
	}

	sub := &pubsubSub{
		messages: make(chan pubsubMessage),
		done:     make(chan struct{}),
		rcloser: func() error {
			return resp.Cancel()
		},
	}

	dec := json.NewDecoder(resp.Output)

	go func() {
		defer close(sub.messages)

		for {
			var msg pubsubMessage
			if err := dec.Decode(&msg); err != nil {
				if err == io.EOF {
					return
				}
				msg.err = err
			}

			select {
			case sub.messages <- msg:
			case <-sub.done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	return sub, nil
}

func (s *pubsubSub) Close() error {
	if s.done != nil {
		close(s.done)
		s.done = nil
	}
	return s.rcloser()
}

func (api *PubsubAPI) core() *HttpApi {
	return (*HttpApi)(api)
}
