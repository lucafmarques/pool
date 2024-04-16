package pool

import (
	"context"
	"sync"
)

func worker(ctx context.Context, wg *sync.WaitGroup, first func(), tasks <-chan func(), executor func(func(), bool)) {
	if first != nil {
		executor(first, true)
	}

	defer wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case task, ok := <-tasks:
			if task == nil || !ok {
				return
			}

			executor(task, false)
		}
	}
}
