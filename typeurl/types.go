package typeurl

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"sync"

	"github.com/gogo/protobuf/proto"
	"github.com/gogo/protobuf/types"
)

const Namespace = "types.containerd.io"

var (
	mu            sync.Mutex
	registry      = make(map[reflect.Type]string)
	ErrRegistered = errors.New("typeurl: type already registred")
	ErrNotExists  = errors.New("typeurl: type is not registered")
)

// Register a type with the base url of the type
func Register(v interface{}, args ...string) {
	t := tryDereference(v)
	mu.Lock()
	defer mu.Unlock()
	if _, ok := registry[t]; ok {
		panic(ErrRegistered)
	}
	registry[t] = filepath.Join(append([]string{Namespace}, args...)...)
}

// TypeUrl returns the type url for a registred type
func TypeUrl(v interface{}) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	u, ok := registry[tryDereference(v)]
	if !ok {
		return "", ErrNotExists
	}
	return u, nil
}

func MarshalAny(v interface{}) (*types.Any, error) {
	var (
		err  error
		data []byte
	)
	url, err := TypeUrl(v)
	if err != nil {
		return nil, err
	}
	switch t := v.(type) {
	case proto.Message:
		data, err = proto.Marshal(t)
	default:
		data, err = json.Marshal(v)
	}
	if err != nil {
		return nil, err
	}
	return &types.Any{
		TypeUrl: url,
		Value:   data,
	}, nil
}

func UnmarshalAny(any *types.Any) (interface{}, error) {
	t, err := getTypeByUrl(any.TypeUrl)
	if err != nil {
		return nil, err
	}
	v := reflect.New(t).Interface()
	switch dt := v.(type) {
	case proto.Message:
		err = proto.Unmarshal(any.Value, dt)
	default:
		err = json.Unmarshal(any.Value, v)
	}
	return v, err
}

func getTypeByUrl(url string) (reflect.Type, error) {
	for t, u := range registry {
		if u == url {
			return t, nil
		}
	}
	return nil, ErrNotExists
}

func tryDereference(v interface{}) reflect.Type {
	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t
}
