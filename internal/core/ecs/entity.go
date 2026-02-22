package ecs

// EntityID encodes a 32-bit index in the lower bits and a 32-bit generation
// in the upper bits. Generation increments on destroy to invalidate stale refs.
type EntityID uint64

func NewEntityID(index uint32, generation uint32) EntityID {
	return EntityID(uint64(generation)<<32 | uint64(index))
}

func (id EntityID) Index() uint32      { return uint32(id) }
func (id EntityID) Generation() uint32 { return uint32(id >> 32) }
func (id EntityID) IsZero() bool       { return id == 0 }

// EntityPool manages entity allocation with generational indices and a free list.
type EntityPool struct {
	generations []uint32
	freeList    []uint32
	nextIndex   uint32
}

func NewEntityPool() *EntityPool {
	return &EntityPool{
		generations: make([]uint32, 0, 1024),
		freeList:    make([]uint32, 0, 256),
	}
}

func (p *EntityPool) Create() EntityID {
	if len(p.freeList) > 0 {
		idx := p.freeList[len(p.freeList)-1]
		p.freeList = p.freeList[:len(p.freeList)-1]
		return NewEntityID(idx, p.generations[idx])
	}
	idx := p.nextIndex
	p.nextIndex++
	if int(idx) >= len(p.generations) {
		p.generations = append(p.generations, 0)
	}
	return NewEntityID(idx, p.generations[idx])
}

func (p *EntityPool) Alive(id EntityID) bool {
	idx := id.Index()
	if idx >= p.nextIndex {
		return false
	}
	return p.generations[idx] == id.Generation()
}

func (p *EntityPool) Destroy(id EntityID) {
	idx := id.Index()
	if idx >= p.nextIndex {
		return
	}
	if p.generations[idx] != id.Generation() {
		return // already destroyed (stale reference)
	}
	p.generations[idx]++
	p.freeList = append(p.freeList, idx)
}
