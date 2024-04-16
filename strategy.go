package pool

import "runtime"

var maxProcs = runtime.GOMAXPROCS(0)

type Strategy interface {
	Resize(running, min, max int) bool
}

var (
	Eager    = func() Strategy { return RateResizer(1) }
	Lazy     = func() Strategy { return RateResizer(maxProcs) }
	Balanced = func() Strategy { return RateResizer(maxProcs / 2) }
)

type resizer struct {
	rate uint64
	hits uint64
}

func RateResizer(rate int) Strategy {
	if rate < 1 {
		rate = 1
	}

	return &resizer{
		rate: uint64(rate),
	}
}

func (r *resizer) Resize(running, min, max int) bool {
	if r.rate == 1 || running == 0 {
		return true
	}

	r.hits++

	return r.hits%r.rate == 1
}
