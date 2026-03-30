package config

import (
	"log"
	"sync"

	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	path     string
	mu       sync.RWMutex
	current  *ClientConfig
	onChange func(*ClientConfig)
	stop     chan struct{}
}

func NewWatcher(path string, onChange func(*ClientConfig)) (*Watcher, error) {
	cfg, err := LoadClientConfig(path)
	if err != nil {
		return nil, err
	}

	return &Watcher{
		path:     path,
		current:  cfg,
		onChange: onChange,
		stop:     make(chan struct{}),
	}, nil
}

func (w *Watcher) Config() *ClientConfig {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.current
}

func (w *Watcher) Start() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	if err := watcher.Add(w.path); err != nil {
		watcher.Close()
		return err
	}

	go func() {
		defer watcher.Close()
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
					w.reload()
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("config watcher error: %v", err)
			case <-w.stop:
				return
			}
		}
	}()

	return nil
}

func (w *Watcher) reload() {
	cfg, err := LoadClientConfig(w.path)
	if err != nil {
		log.Printf("config reload failed (keeping previous): %v", err)
		return
	}

	w.mu.Lock()
	w.current = cfg
	w.mu.Unlock()

	log.Printf("config reloaded: %d routes", len(cfg.Routes))
	if w.onChange != nil {
		w.onChange(cfg)
	}
}

func (w *Watcher) Stop() {
	select {
	case <-w.stop:
	default:
		close(w.stop)
	}
}
