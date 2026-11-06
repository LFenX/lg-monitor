package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type fileWatcher struct {
	native    *fsnotify.Watcher
	directory string
	changes   chan struct{}
	errors    chan error
	stop      chan struct{}
	done      chan struct{}
	once      sync.Once
	closeErr  error
}

func NewFileWatcher(directory string) (ChangeSource, error) {
	directory, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("解析配置目录失败：%w", err)
	}
	if err := os.MkdirAll(filepath.Join(directory, "card-templates"), 0o755); err != nil {
		return nil, fmt.Errorf("创建配置监听目录失败：%w", err)
	}
	native, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("创建配置监听器失败：%w", err)
	}
	watcher := &fileWatcher{
		native: native, directory: directory,
		changes: make(chan struct{}, 1), errors: make(chan error, 1),
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	for _, dir := range []string{filepath.Dir(directory), directory, filepath.Join(directory, "card-templates")} {
		if err := native.Add(dir); err != nil {
			_ = native.Close()
			return nil, fmt.Errorf("监听配置目录失败：%w", err)
		}
	}
	go watcher.run()
	return watcher, nil
}

func (w *fileWatcher) Changes() <-chan struct{} { return w.changes }
func (w *fileWatcher) Errors() <-chan error     { return w.errors }

func (w *fileWatcher) Close() error {
	w.once.Do(func() {
		close(w.stop)
		<-w.done
		w.closeErr = w.native.Close()
	})
	return w.closeErr
}

func (w *fileWatcher) report(err error) {
	select {
	case w.errors <- err:
	default:
	}
}

func (w *fileWatcher) run() {
	defer close(w.done)
	defer close(w.changes)
	defer close(w.errors)
	var timer *time.Timer
	var timerC <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case <-w.stop:
			return
		case failure, ok := <-w.native.Errors:
			if !ok {
				return
			}
			w.report(fmt.Errorf("配置监听器错误：%w", failure))
		case event, ok := <-w.native.Events:
			if !ok {
				return
			}
			if !w.relevant(event.Name) {
				continue
			}
			if event.Has(fsnotify.Create) {
				for _, dir := range []string{w.directory, filepath.Join(w.directory, "card-templates")} {
					if info, err := os.Stat(dir); err == nil && info.IsDir() {
						if err := w.native.Add(dir); err != nil {
							w.report(fmt.Errorf("重新监听配置目录失败：%w", err))
						}
					}
				}
			}
			// Editors commonly replace files and emit several events per save.
			if timer == nil {
				timer = time.NewTimer(100 * time.Millisecond)
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(100 * time.Millisecond)
			}
			timerC = timer.C
		case <-timerC:
			timerC = nil
			select {
			case w.changes <- struct{}{}:
			default:
			}
		}
	}
}

func (w *fileWatcher) relevant(name string) bool {
	name = filepath.Clean(name)
	if strings.EqualFold(name, w.directory) || strings.EqualFold(name, filepath.Join(w.directory, "card-templates")) {
		return true
	}
	if strings.EqualFold(filepath.Dir(name), w.directory) {
		return oneOf(strings.ToLower(filepath.Base(name)), "targets.yaml", "rules.yaml", "notifiers.yaml", "settings.yaml")
	}
	return strings.EqualFold(filepath.Dir(name), filepath.Join(w.directory, "card-templates")) && oneOf(strings.ToLower(filepath.Ext(name)), ".yaml", ".yml")
}
