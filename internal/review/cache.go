package review

import (
	"container/list"
	"sync"

	"github.com/0xmhha/garret/internal/gitrepo"
)

// Diff cache limits (services.md, Q4).
const (
	cacheEntries = 256
	cacheBytes   = 64 << 20
	// lineOverhead approximates the memory of a Line beyond its text.
	lineOverhead = 48
)

type cacheKey struct{ from, to, oldPath, newPath string }

type cacheEntry struct {
	key   cacheKey
	hunks []gitrepo.Hunk
	size  int
}

// diffCache is a least-recently-used cache of file diffs. Commit names make
// cached diffs immutable, so entries never need invalidation. It is safe for
// concurrent use.
type diffCache struct {
	mu    sync.Mutex
	order *list.List // front = most recently used
	items map[cacheKey]*list.Element
	bytes int
}

func newDiffCache() *diffCache {
	return &diffCache{order: list.New(), items: map[cacheKey]*list.Element{}}
}

func (c *diffCache) get(k cacheKey) ([]gitrepo.Hunk, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[k]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*cacheEntry).hunks, true
}

func (c *diffCache) put(k cacheKey, hunks []gitrepo.Hunk) {
	size := hunkSize(hunks)
	if size > cacheBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[k]; ok {
		c.order.MoveToFront(el)
		return
	}
	c.items[k] = c.order.PushFront(&cacheEntry{key: k, hunks: hunks, size: size})
	c.bytes += size
	for c.order.Len() > cacheEntries || c.bytes > cacheBytes {
		oldest := c.order.Back()
		e := oldest.Value.(*cacheEntry)
		c.order.Remove(oldest)
		delete(c.items, e.key)
		c.bytes -= e.size
	}
}

func (c *diffCache) stats() (entries, bytes int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len(), c.bytes
}

func hunkSize(hunks []gitrepo.Hunk) int {
	n := 0
	for _, h := range hunks {
		n += len(h.Section)
		for _, l := range h.Lines {
			n += len(l.Text) + lineOverhead
		}
	}
	return n
}
