package pool

import (
	"context"
	"sync"
)

type Group struct {
	pool *Pool
	ctx  context.Context
	wg   sync.WaitGroup
}

func (g *Group) Submit(task func()) {
	g.wg.Add(1)

	g.pool.submit(g.ctx, true, func() {
		task()
		g.wg.Done()
	})
}

func (g *Group) Wait() {
	g.wg.Wait()
}
