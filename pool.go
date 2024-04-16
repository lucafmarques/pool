package pool

type Pool struct{}

type Option func(*Pool)

func New() Pool { return Pool{} }
