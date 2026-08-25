package faults

import "context"

type Hook interface {
	Reach(context.Context, string) error
}

type Noop struct{}

func (Noop) Reach(context.Context, string) error { return nil }
