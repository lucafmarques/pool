package pool

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type Option func(*Pool)

func WithContext(ctx context.Context) Option {
	return func(p *Pool) {
		p.ctx = ctx
	}
}

func WithPanicHandler(handler func(any)) Option {
	return func(p *Pool) {
		p.panicHandler = handler
	}
}

func WithStrategy(strategy Strategy) Option {
	return func(p *Pool) {
		p.strategy = strategy
	}
}

type Pool struct {
	// Atomic counters
	activeWorkerCount uint32
	idleWorkerCount   uint32
	pendingCount      uint64
	submittedCount    uint64
	failedCount       uint64
	successfulCount   uint64
	// Configurable options
	maxWorkers   int
	minWorkers   int
	maxCapacity  int
	strategy     Strategy
	idleTimeout  time.Duration
	panicHandler func(any)
	ctx          context.Context
	ctxcancel    context.CancelFunc
	// Privates
	close    sync.Once
	tasks    chan func()
	tasksWG  sync.WaitGroup
	workerWG sync.WaitGroup
	mutex    sync.Mutex
}

func New(max, cap int, opts ...Option) *Pool {
	var timeout = 10 * time.Second

	pool := &Pool{
		strategy:    Eager(),
		maxWorkers:  max,
		maxCapacity: cap,
		idleTimeout: timeout,
		panicHandler: func(panic any) {
			fmt.Printf("worker panicked: %v", panic)
		},
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
		WithContext(context.Background())(pool)
	}

	pool.tasks = make(chan func(), pool.maxCapacity)
	if pool.minWorkers > 0 {
		for range pool.minWorkers {
			pool.work(nil)
		}
	}

	return pool
}

func (p *Pool) Submit(task func()) {
	p.submit(task, true)
}

func (p *Pool) TrySubmit(task func()) bool {
	return p.submit(task, false)
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

func (p *Pool) Group() *Group {
	return &Group{
		pool: p,
	}
}

func (p *Pool) ActiveWorkers() int {
	return int(atomic.LoadUint32(&p.activeWorkerCount))
}

func (p *Pool) IdleWorkers() int {
	return int(atomic.LoadUint32(&p.idleWorkerCount))
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

	if !p.strategy.Resize(int(p.activeWorkerCount), p.minWorkers, p.maxWorkers) {
		return false
	}

	atomic.AddUint32(&p.activeWorkerCount, 1)
	p.workerWG.Add(1)

	return true
}

func (p *Pool) submit(task func(), must bool) (sent bool) {
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

	if sent = p.work(task); sent {
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

func (p *Pool) work(task func()) bool {
	if !p.incrementWorkers() {
		return false
	}
	atomic.AddUint32(&p.activeWorkerCount, 1)
	if task == nil {
		atomic.AddUint32(&p.idleWorkerCount, 1)
	}

	go worker(p.ctx, &p.workerWG, task, p.tasks, p.execute)

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
