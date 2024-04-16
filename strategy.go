package pool

type Strategy interface {
	Resize(running, min, max int) bool
}
