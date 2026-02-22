package ecs

// Registry tracks all component stores and supports bulk cleanup on entity destroy.
type Registry struct {
	stores []Removable
}

func NewRegistry() *Registry {
	return &Registry{
		stores: make([]Removable, 0, 16),
	}
}

// Register adds a component store to the registry.
func (r *Registry) Register(store Removable) {
	r.stores = append(r.stores, store)
}

// RemoveAll clears the given entity from every registered component store.
func (r *Registry) RemoveAll(id EntityID) {
	for _, s := range r.stores {
		s.Remove(id)
	}
}
