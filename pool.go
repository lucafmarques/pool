package pool

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type Pool struct {
	// Atomic counters
	activeWorkerCount uint32
	idleWorkerCount   uint32
	pendingCount      uint64
	submittedCount    uint64
	failedCount       uint64
	successfulCount   uint64
	stopped           uint64

	// Configurable options
	idleTimeout  time.Duration
	maxCapacity  int
	maxWorkers   int
	minWorkers   int
	strat        Strategy
	ctx          context.Context
	ctxcancel    context.CancelFunc
	panicHandler func(any)

	// Privates
	tasks    chan func()
	mutex    sync.Mutex
	tasksWG  sync.WaitGroup
	workerWG sync.WaitGroup
	close    sync.Once
}

func New(ctx context.Context, opts ...Option) *Pool {
	var timeout = 10 * time.Second

	pool := &Pool{
		strat:       Eager(),
		maxWorkers:  int(^uint(0) >> 1),
		maxCapacity: int(^uint(0) >> 1),
		idleTimeout: timeout,
		panicHandler: func(panic any) {
			fmt.Printf("worker panicked: %v", panic)
		},
	}

	for _, opt := range opts {
		opt(pool)
	}

	if pool.maxWorkers < 1 {
		pool.maxWorkers = 1
	}

	if pool.maxCapacity < 0 {
		pool.maxCapacity = 0
	}

	if pool.minWorkers > pool.maxWorkers {
		pool.minWorkers = pool.maxWorkers
	}

	if pool.idleTimeout < 0 {
		pool.idleTimeout = timeout
	}

	if pool.ctx == nil {
		pool.ctx = context.Background()
	}

	if pool.ctxcancel == nil {
		pool.ctx, pool.ctxcancel = context.WithCancel(pool.ctx)
	}

	go pool.clean()

	pool.tasks = make(chan func(), pool.maxCapacity)
	if pool.minWorkers > 0 {
		for range pool.minWorkers {
			pool.work(pool.ctx, nil)
		}
	}

	return pool
}

func (p *Pool) Submit(task func()) {
	p.submit(p.ctx, true, task)
}

func (p *Pool) TrySubmit(task func()) bool {
	return p.submit(p.ctx, false, task)
}

func (p *Pool) WaitSubmit(task func()) {
	if task == nil {
		return
	}

	done := make(chan struct{})
	p.Submit(func() {
		task()
		close(done)
	})
	<-done
}

func (p *Pool) Stop() {
	go p.stop(false)
}

func (p *Pool) WaitStop() {
	p.stop(true)
}

func (p *Pool) Group(ctx context.Context) *Group {
	return &Group{
		pool: p,
		ctx:  ctx,
	}
}

func (p *Pool) IdleWorkers() int {
	return int(atomic.LoadUint32(&p.idleWorkerCount))
}

func (p *Pool) ActiveWorkers() int {
	return int(atomic.LoadUint32(&p.activeWorkerCount))
}

func (p *Pool) PendingTasks() int {
	return int(atomic.LoadUint64(&p.pendingCount))
}

func (p *Pool) SubmittedTasks() int {
	return int(atomic.LoadUint64(&p.submittedCount))
}

func (p *Pool) FailedTasks() int {
	return int(atomic.LoadUint64(&p.failedCount))
}

func (p *Pool) SuccessfulTasks() int {
	return int(atomic.LoadUint64(&p.successfulCount))
}

func (p *Pool) FinishedTasks() int {
	return p.FailedTasks() + p.SuccessfulTasks()
}

func (p *Pool) Stopped() bool {
	return atomic.LoadUint64(&p.stopped) == 1
}

func (p *Pool) incrementWorkers() bool {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	active := p.ActiveWorkers()

	if active >= p.maxWorkers {
		return false
	}

	if active >= p.minWorkers && active > 0 && p.IdleWorkers() > 0 {
		return false
	}

	if !p.strat.Resize(int(p.activeWorkerCount), p.minWorkers, p.maxWorkers) {
		return false
	}

	atomic.AddUint32(&p.activeWorkerCount, 1)
	p.workerWG.Add(1)

	return true
}

func (p *Pool) decrementWorkers() bool {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if p.IdleWorkers() <= 0 || p.ActiveWorkers() <= p.minWorkers || p.Stopped() {
		return false
	}

	atomic.AddUint32(&p.idleWorkerCount, ^uint32(0))
	atomic.AddUint32(&p.activeWorkerCount, ^uint32(0))

	p.tasks <- nil

	return true
}

func (p *Pool) submit(ctx context.Context, must bool, task func()) (sent bool) {
	if task == nil {
		return false
	}

	atomic.AddUint64(&p.pendingCount, 1)
	atomic.AddUint64(&p.submittedCount, 1)
	p.tasksWG.Add(1)

	defer func() {
		if !sent {
			atomic.AddUint64(&p.pendingCount, ^uint64(0))
			atomic.AddUint64(&p.submittedCount, ^uint64(0))
			p.tasksWG.Done()
		}
	}()

	if sent = p.work(ctx, task); sent {
		return sent
	}

	if !must {
		select {
		case p.tasks <- task:
			return true
		default:
			return false
		}
	}

	p.tasks <- task
	return true
}

func (p *Pool) work(ctx context.Context, task func()) bool {
	if !p.incrementWorkers() {
		return false
	}
	atomic.AddUint32(&p.activeWorkerCount, 1)
	if task == nil {
		atomic.AddUint32(&p.idleWorkerCount, 1)
	}

	go worker(ctx, &p.workerWG, task, p.tasks, p.execute)

	return true
}

func (p *Pool) execute(task func(), first bool) {
	defer func() {
		if panic := recover(); panic != nil {
			atomic.AddUint64(&p.failedCount, 1)
			p.panicHandler(panic)
			atomic.AddUint32(&p.idleWorkerCount, 1)
		}
	}()

	if !first {
		atomic.AddUint32(&p.idleWorkerCount, ^uint32(0))
	}

	atomic.AddUint64(&p.pendingCount, ^uint64(0))

	task()

	atomic.AddUint64(&p.successfulCount, 1)
	atomic.AddUint32(&p.idleWorkerCount, 1)
}

func (p *Pool) clean() {
	p.workerWG.Add(1)
	defer p.workerWG.Done()

	ticker := time.NewTicker(p.idleTimeout)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.decrementWorkers()
		case <-p.ctx.Done():
			return
		}
	}
}

func (p *Pool) stop(wait bool) {
	atomic.StoreUint64(&p.stopped, 1)

	if wait {
		p.tasksWG.Wait()
	}

	p.mutex.Lock()
	atomic.StoreUint32(&p.activeWorkerCount, 0)
	atomic.StoreUint32(&p.idleWorkerCount, 0)
	p.mutex.Unlock()

	p.ctxcancel()

	p.workerWG.Wait()
	p.close.Do(func() {
		close(p.tasks)
	})
}
