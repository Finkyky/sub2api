package codexclient

import (
	"sync"

	utls "github.com/refraction-networking/utls"
)

type clientSessionCache struct {
	mu    sync.Mutex
	cache utls.ClientSessionCache
}

func newClientSessionCache() *clientSessionCache {
	return &clientSessionCache{cache: utls.NewLRUClientSessionCache(256)}
}

func (cache *clientSessionCache) Get(key string) (*utls.ClientSessionState, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	state, found := cache.cache.Get(key)
	if state == nil {
		if found {
			cache.cache.Put(key, nil)
		}
		return nil, false
	}
	if found && state.Vers() == utls.VersionTLS13 {
		cache.cache.Put(key, nil)
	}
	return state, found
}

func (cache *clientSessionCache) Put(key string, state *utls.ClientSessionState) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if state == nil {
		if _, found := cache.cache.Get(key); !found {
			return
		}
	}
	cache.cache.Put(key, state)
}
