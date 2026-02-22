package ecs

// Removable is implemented by all component stores so the Registry can
// bulk-remove an entity's data from every store on destroy.
type Removable interface {
	Remove(id EntityID)
}

// PtrComponentStore is a generic typed map store for ECS components.
// No reflect, no interface{} — pure generics.
type PtrComponentStore[T any] struct {
	data map[EntityID]*T
}

func NewPtrComponentStore[T any]() *PtrComponentStore[T] {
	return &PtrComponentStore[T]{
		data: make(map[EntityID]*T, 256),
	}
}

func (s *PtrComponentStore[T]) Set(id EntityID, c *T) {
	s.data[id] = c
}

func (s *PtrComponentStore[T]) Get(id EntityID) (*T, bool) {
	c, ok := s.data[id]
	return c, ok
}

func (s *PtrComponentStore[T]) Remove(id EntityID) {
	delete(s.data, id)
}

func (s *PtrComponentStore[T]) Has(id EntityID) bool {
	_, ok := s.data[id]
	return ok
}

func (s *PtrComponentStore[T]) Len() int {
	return len(s.data)
}

func (s *PtrComponentStore[T]) Each(fn func(EntityID, *T)) {
	for id, c := range s.data {
		fn(id, c)
	}
}
