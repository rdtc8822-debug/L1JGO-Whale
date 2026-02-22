package ecs

// World is the top-level ECS container. It owns the entity pool, the component
// registry, and a deferred destruction queue flushed by CleanupSystem each tick.
type World struct {
	pool         *EntityPool
	registry     *Registry
	destroyQueue []EntityID
}

func NewWorld() *World {
	return &World{
		pool:         NewEntityPool(),
		registry:     NewRegistry(),
		destroyQueue: make([]EntityID, 0, 64),
	}
}

func (w *World) Pool() *EntityPool   { return w.pool }
func (w *World) Registry() *Registry { return w.registry }

func (w *World) CreateEntity() EntityID {
	return w.pool.Create()
}

func (w *World) Alive(id EntityID) bool {
	return w.pool.Alive(id)
}

// MarkForDestruction queues an entity for end-of-tick cleanup.
func (w *World) MarkForDestruction(id EntityID) {
	w.destroyQueue = append(w.destroyQueue, id)
}

// FlushDestroyQueue destroys all queued entities and clears their components.
// Called by CleanupSystem at the end of each tick.
func (w *World) FlushDestroyQueue() {
	for _, id := range w.destroyQueue {
		w.registry.RemoveAll(id)
		w.pool.Destroy(id)
	}
	w.destroyQueue = w.destroyQueue[:0]
}
