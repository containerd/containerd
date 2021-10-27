package httpapi

import (
	"context"
	"io"
	"strings"
)

type Request struct {
	Ctx     context.Context
	ApiBase string
	Command string
	Args    []string
	Opts    map[string]string
	Body    io.Reader
	Headers map[string]string
}

func NewRequest(ctx context.Context, url, command string, args ...string) *Request {
	if !strings.HasPrefix(url, "http") {
		url = "http://" + url
	}

	opts := map[string]string{
		"encoding":        "json",
		"stream-channels": "true",
	}
	return &Request{
		Ctx:     ctx,
		ApiBase: url + "/api/v0",
		Command: command,
		Args:    args,
		Opts:    opts,
		Headers: make(map[string]string),
	}
}
