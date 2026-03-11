package tools

import (
	"context"
	"fmt"
)

type EchoTool struct{}

func NewEchoTool() *EchoTool { return &EchoTool{} }

func (t *EchoTool) Name() string { return "echo" }

func (t *EchoTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	_ = ctx
	v, ok := args["text"]
	if !ok {
		v = args["input"]
	}
	if v == nil {
		return "", nil
	}
	return fmt.Sprint(v), nil
}
