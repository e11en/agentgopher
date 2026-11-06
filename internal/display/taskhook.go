package display

import "time"

// TaskEvent is one finished Task: its title, when it ran and its error.
type TaskEvent struct {
	Title      string
	Start, End time.Time
	Err        error
}

type taskHook struct {
	Display
	fn func(TaskEvent)
}

// WithTaskHook returns d with fn called after every Task it runs.
func WithTaskHook(d Display, fn func(TaskEvent)) Display { return taskHook{d, fn} }

func (t taskHook) Task(title string, fn func(log func(string)) error) error {
	start := time.Now()
	err := t.Display.Task(title, fn)
	t.fn(TaskEvent{Title: title, Start: start, End: time.Now(), Err: err})
	return err
}
