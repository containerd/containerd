package verifier

import "context"

type Verifier interface {
	VerifyImage(ctx context.Context, name string, digest string) (Judgement, error)
}

type Judgement struct {
	OK     bool
	Reason string
}
