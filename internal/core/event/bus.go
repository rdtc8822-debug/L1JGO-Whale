package event

import (
	"reflect"
	"sync"
)

// Bus is a double-buffered event bus. Events emitted in tick N are readable
// in tick N+1. SwapBuffers() is called at tick start by EventDispatchSystem.
type Bus struct {
	mu       sync.Mutex // only protects handler registration
	front    map[reflect.Type][]any
	back     map[reflect.Type][]any
	handlers map[reflect.Type][]any
}

func NewBus() *Bus {
	return &Bus{
		front:    make(map[reflect.Type][]any),
		back:     make(map[reflect.Type][]any),
		handlers: make(map[reflect.Type][]any),
	}
}

// Emit queues an event into the back buffer (will be readable next tick).
func Emit[T any](b *Bus, event T) {
	t := reflect.TypeOf((*T)(nil)).Elem()
	b.back[t] = append(b.back[t], event)
}

// Subscribe registers a typed handler for events of type T.
func Subscribe[T any](b *Bus, fn func(T)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	t := reflect.TypeOf((*T)(nil)).Elem()
	b.handlers[t] = append(b.handlers[t], fn)
}

// SwapBuffers rotates back→front and clears the new back buffer.
// Called once at tick start.
func (b *Bus) SwapBuffers() {
	b.front, b.back = b.back, b.front
	for k := range b.back {
		b.back[k] = b.back[k][:0]
	}
}

// DispatchAll delivers all front-buffer events to their subscribed handlers.
func (b *Bus) DispatchAll() {
	for t, events := range b.front {
		handlers := b.handlers[t]
		for _, ev := range events {
			for _, h := range handlers {
				// Type-assert the handler and call it.
				// This is safe because Subscribe and Emit use the same type key.
				callHandler(h, ev)
			}
		}
	}
}

func callHandler(handler any, event any) {
	reflect.ValueOf(handler).Call([]reflect.Value{reflect.ValueOf(event)})
}
