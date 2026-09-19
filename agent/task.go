package agent

import (
	"errors"

	"agent/feature/task"
)

// errTaskDisabled возвращается, когда feature состояния задачи не настроен
// (config.task_file пуст).
var errTaskDisabled = errors.New("состояние задачи не настроено (task_file в config.json)")

// Task возвращает конечный автомат состояния задачи (nil — feature выключен).
func (a *Agent) Task() task.Machine {
	if a.tasks == nil {
		return nil
	}
	return a.tasks
}

// TaskState возвращает снапшот состояния активной задачи.
func (a *Agent) TaskState() task.State {
	if a.tasks == nil {
		return task.State{}
	}
	return a.tasks.Snapshot()
}

// BeginTask открывает новую задачу с целью goal (переход в planning).
// Возвращает ошибку, если feature состояния задачи не настроен.
func (a *Agent) BeginTask(goal string) error {
	if a.tasks == nil {
		return errTaskDisabled
	}
	return a.tasks.Begin(goal)
}

// TaskEnabled сообщает, настроено ли состояние задачи (task_file).
func (a *Agent) TaskEnabled() bool { return a.tasks != nil }
