// +build linux

package linux

import (
	"context"
	"sync"

	"github.com/containerd/containerd/namespaces"
)

func newTaskList() *taskList {
	return &taskList{
		tasks: make(map[string]map[string]*Task),
	}
}

type taskList struct {
	mu    sync.Mutex
	tasks map[string]map[string]*Task
}

func (l *taskList) get(ctx context.Context, id string) (*Task, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}
	tasks, ok := l.tasks[namespace]
	if !ok {
		return nil, ErrTaskNotExists
	}
	t, ok := tasks[id]
	if !ok {
		return nil, ErrTaskNotExists
	}
	return t, nil
}

func (l *taskList) add(ctx context.Context, t *Task) error {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}
	return l.addWithNamespace(namespace, t)
}

func (l *taskList) addWithNamespace(namespace string, t *Task) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	id := t.containerID
	if _, ok := l.tasks[namespace]; !ok {
		l.tasks[namespace] = make(map[string]*Task)
	}
	if _, ok := l.tasks[namespace][id]; ok {
		return ErrTaskAlreadyExists
	}
	l.tasks[namespace][id] = t
	return nil
}

func (l *taskList) delete(ctx context.Context, t *Task) {
	l.mu.Lock()
	defer l.mu.Unlock()
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return
	}
	tasks, ok := l.tasks[namespace]
	if ok {
		delete(tasks, t.containerID)
	}
}
