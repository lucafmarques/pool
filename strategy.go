package pool

import "runtime"

var maxProcs = runtime.GOMAXPROCS(0)

var (
	Eager    = func() Strategy { return RatedResizer(1) }
	Lazy     = func() Strategy { return RatedResizer(maxProcs) }
	Balanced = func() Strategy { return RatedResizer(maxProcs / 2) }
)

type Strategy interface {
	Resize(running, min, max int) bool
}

type ratedResizer struct {
	rate uint64
	hits uint64
}

func (r *ratedResizer) Resize(running, min, max int) bool {
	if r.rate == 1 || running == 0 {
		return true
	}

	r.hits++

	return r.hits%r.rate == 1
}

func RatedResizer(rate int) Strategy {
	if rate < 1 {
		rate = 1
	}

	return &ratedResizer{
		rate: uint64(rate),
	}
}
