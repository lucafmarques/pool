package pool

import "time"

type Option func(*Pool)

func WithLimits(min, max, cap int) Option {
	return func(p *Pool) {
		p.maxCapacity = cap
		p.minWorkers = min
		p.maxWorkers = max
	}
}

func WithPanicHandler(handler func(any)) Option {
	return func(p *Pool) {
		p.panicHandler = handler
	}
}

func WithStrategy(strategy Strategy) Option {
	return func(p *Pool) {
		p.strat = strategy
	}
}

func WithTimeout(timeout time.Duration) Option {
	return func(p *Pool) {
		p.idleTimeout = timeout
	}
}
