package leases

import "time"

type LeaseOpt func(*Lease)

type LeaseManager interface {
	Create(...LeaseOpt) (Lease, error)
	Delete(Lease) error
	List(...string) ([]Lease, error)
}

type Lease struct {
	ID        string
	CreatedAt time.Time
	Labels    map[string]string
}
