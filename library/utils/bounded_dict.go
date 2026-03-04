package utils

import (
	"container/list"
	"sync"
)

type BoundedDict[K comparable, V any] struct {
	maxSize int
	evictList *list.List
	items     map[K]*list.Element
	mu        sync.Mutex
}

type entry[K comparable, V any] struct {
	key   K
	value V
}

func NewBoundedDict[K comparable, V any](maxSize int) *BoundedDict[K, V] {
	return &BoundedDict[K, V]{
		maxSize:   maxSize,
		evictList: list.New(),
		items:     make(map[K]*list.Element),
	}
}

func (c *BoundedDict[K, V]) Set(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ent, ok := c.items[key]; ok {
		c.evictList.MoveToFront(ent)
		ent.Value.(*entry[K, V]).value = value
		return
	}

	ent := &entry[K, V]{key, value}
	element := c.evictList.PushFront(ent)
	c.items[key] = element

	if c.evictList.Len() > c.maxSize {
		c.removeOldest()
	}
}

func (c *BoundedDict[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ent, ok := c.items[key]; ok {
		c.evictList.MoveToFront(ent)
		return ent.Value.(*entry[K, V]).value, true
	}

	var zero V
	return zero, false
}

func (c *BoundedDict[K, V]) removeOldest() {
	ent := c.evictList.Back()
	if ent != nil {
		c.evictList.Remove(ent)
		kv := ent.Value.(*entry[K, V])
		delete(c.items, kv.key)
	}
}
