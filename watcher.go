package main

import (
	"time"

	"github.com/fsnotify/fsnotify"
)

// watcher is a simple wrapper over fsnotify.Watcher that adds some local
// convenience functions for watching files specific to this app.
type watcher fsnotify.Watcher

func NewWatcher(paths ...string) (*watcher, error) {
	// todo: skip if using embedded files

	watcher0, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		err = watcher0.Add(path)
		if err != nil {
			return nil, err
		}
	}
	return (*watcher)(watcher0), nil
}

func (w *watcher) Close() {
	(*fsnotify.Watcher)(w).Close()
}

func (w *watcher) Debounce(delay time.Duration) {
	t := time.NewTimer(delay)
	watcher0 := (*fsnotify.Watcher)(w)
debounce:
	for {
		select {
		case <-watcher0.Events:
			if !t.Stop() {
				<-t.C
				break debounce
			}
			t.Reset(delay)
			continue
		case <-t.C:
			break debounce
		}
	}
}
