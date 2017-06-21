package containers

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"time"
)

// Container represents the set of data pinned by a container. Unless otherwise
// noted, the resources here are considered in use by the container.
//
// The resources specified in this object are used to create tasks from the container.
type Container struct {
	ID        string
	Labels    map[string]string
	Image     string
	Runtime   RuntimeInfo
	Spec      []byte
	RootFS    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type RuntimeInfo struct {
	Name    string
	Options map[string]string
}

type marshaledRuntimeInfo struct {
	Name    string
	Options map[string]string
}

func (r *RuntimeInfo) MarshalBinary() ([]byte, error) {
	buf := bytes.NewBuffer(nil)
	if err := gob.NewEncoder(buf).Encode(marshaledRuntimeInfo{
		Name:    r.Name,
		Options: r.Options,
	}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (r *RuntimeInfo) UnmarshalBinary(data []byte) error {
	buf := data
	if len(buf) == 0 {
		return errors.New("RuntimeInfo: no data")
	}
	var (
		mr     marshaledRuntimeInfo
		reader = bytes.NewReader(buf)
	)
	if err := gob.NewDecoder(reader).Decode(&mr); err != nil {
		return err
	}
	r.Name = mr.Name
	r.Options = mr.Options
	return nil
}

type Store interface {
	Get(ctx context.Context, id string) (Container, error)
	List(ctx context.Context, filter string) ([]Container, error)
	Create(ctx context.Context, container Container) (Container, error)
	Update(ctx context.Context, container Container) (Container, error)
	Delete(ctx context.Context, id string) error
}
