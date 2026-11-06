package display

import (
	"errors"
	"io"
	"testing"
)

func TestWithTaskHook(t *testing.T) {
	var got []TaskEvent
	d := WithTaskHook(NewWriter(io.Discard), func(e TaskEvent) { got = append(got, e) })
	boom := errors.New("boom")
	_ = d.Task("ok", func(func(string)) error { return nil })
	if err := d.Task("fail", func(func(string)) error { return boom }); err != boom {
		t.Fatalf("err = %v, want boom", err)
	}
	if len(got) != 2 || got[0].Title != "ok" || got[0].Err != nil || got[1].Title != "fail" || got[1].Err != boom || got[0].End.Before(got[0].Start) {
		t.Fatalf("events = %+v", got)
	}
}
