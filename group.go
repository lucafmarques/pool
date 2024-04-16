package pool

import "sync"

type Group struct {
	pool *Pool
	wg   sync.WaitGroup
}

func (g *Group) Submit(task func()) {
	g.wg.Add(1)
	g.pool.Submit(func() {
		task()
		g.wg.Done()
	})
}

func (g *Group) Wait() {
	g.wg.Wait()
}
