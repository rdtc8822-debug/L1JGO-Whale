package ecs

// Each2 iterates over entities that have both component A and B.
// It iterates over the smaller store and checks the larger one.
func Each2[A, B any](sa *PtrComponentStore[A], sb *PtrComponentStore[B], fn func(EntityID, *A, *B)) {
	if sa.Len() <= sb.Len() {
		for id, a := range sa.data {
			if b, ok := sb.data[id]; ok {
				fn(id, a, b)
			}
		}
	} else {
		for id, b := range sb.data {
			if a, ok := sa.data[id]; ok {
				fn(id, a, b)
			}
		}
	}
}

// Each3 iterates over entities that have components A, B, and C.
func Each3[A, B, C any](sa *PtrComponentStore[A], sb *PtrComponentStore[B], sc *PtrComponentStore[C], fn func(EntityID, *A, *B, *C)) {
	// Iterate the smallest store
	smallest := sa.Len()
	which := 0
	if sb.Len() < smallest {
		smallest = sb.Len()
		which = 1
	}
	if sc.Len() < smallest {
		which = 2
	}

	switch which {
	case 0:
		for id, a := range sa.data {
			if b, ok := sb.data[id]; ok {
				if c, ok := sc.data[id]; ok {
					fn(id, a, b, c)
				}
			}
		}
	case 1:
		for id, b := range sb.data {
			if a, ok := sa.data[id]; ok {
				if c, ok := sc.data[id]; ok {
					fn(id, a, b, c)
				}
			}
		}
	case 2:
		for id, c := range sc.data {
			if a, ok := sa.data[id]; ok {
				if b, ok := sb.data[id]; ok {
					fn(id, a, b, c)
				}
			}
		}
	}
}
