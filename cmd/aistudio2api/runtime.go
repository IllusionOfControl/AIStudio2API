package main

import (
	"context"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
	"github.com/Mag1cFall/AIStudio2API/internal/orchestrator"
)

func newRuntime(ctx context.Context, cfg config.Config) (aistudio.Service, *runtimeAdmin, func() error, error) {
	engine, err := orchestrator.NewEngine(ctx, cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	admin := newRuntimeAdmin(engine)
	return engine.Service, admin, engine.Close, nil
}
