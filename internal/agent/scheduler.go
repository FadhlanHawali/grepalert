package agent

import (
	"container/heap"
	"sync"
	"time"
)

// scheduler polls many tailers with a small, fixed pool of workers instead of
// one goroutine per file. With hundreds of files this keeps memory flat (a
// goroutine stack grows to tens of KB while matching; a tailer's state is
// ~1.5 KB) and caps how many cores the agent uses, so catching up on a large
// backlog cannot starve the applications on the same machine.
//
// Each tailer is run by at most one worker at a time. Busy files are polled
// every poll interval; files with no new data back off up to idlePoll; a file
// whose read budget ran out (a backlog) is requeued immediately, behind any
// other file that is due, so one huge file cannot delay alerts on the others.
type scheduler struct {
	poll, idlePoll time.Duration

	mu      sync.Mutex
	queue   entryHeap
	entries map[*tailer]*entry
	stopped bool

	wake chan struct{}
	work chan *entry
	wg   sync.WaitGroup
	done chan struct{}
	now  func() time.Time
}

type entry struct {
	t        *tailer
	next     time.Time
	interval time.Duration
	index    int  // position in queue, -1 when not queued
	running  bool // a worker is running it
	removed  bool
}

func newScheduler(workers int, poll, idlePoll time.Duration) *scheduler {
	if workers < 1 {
		workers = 1
	}
	s := &scheduler{
		poll:     poll,
		idlePoll: max(idlePoll, poll),
		entries:  map[*tailer]*entry{},
		wake:     make(chan struct{}, 1),
		work:     make(chan *entry),
		done:     make(chan struct{}),
		now:      time.Now,
	}
	s.wg.Add(1)
	go s.dispatch()
	for i := 0; i < workers; i++ {
		s.wg.Add(1)
		go s.worker()
	}
	return s
}

func (s *scheduler) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// add starts polling t immediately.
func (s *scheduler) add(t *tailer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || s.entries[t] != nil {
		return
	}
	e := &entry{t: t, next: s.now(), interval: s.poll}
	s.entries[t] = e
	heap.Push(&s.queue, e)
	s.signal()
}

// remove stops polling t and closes its file (now, or when its current run ends).
func (s *scheduler) remove(t *tailer) {
	s.mu.Lock()
	e := s.entries[t]
	if e == nil {
		s.mu.Unlock()
		return
	}
	delete(s.entries, t)
	e.removed = true
	running := e.running
	if e.index >= 0 {
		heap.Remove(&s.queue, e.index)
	}
	s.mu.Unlock()
	if !running {
		t.close()
	}
}

func (s *scheduler) dispatch() {
	defer s.wg.Done()
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		s.mu.Lock()
		var due *entry
		wait := time.Hour
		if len(s.queue) > 0 {
			if d := s.queue[0].next.Sub(s.now()); d <= 0 {
				due = heap.Pop(&s.queue).(*entry)
				due.running = true
			} else {
				wait = d
			}
		}
		s.mu.Unlock()

		if due != nil {
			select {
			case s.work <- due:
				continue
			case <-s.done:
				return
			}
		}
		timer.Reset(wait)
		select {
		case <-timer.C:
		case <-s.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-s.done:
			return
		}
	}
}

func (s *scheduler) worker() {
	defer s.wg.Done()
	for {
		var e *entry
		select {
		case e = <-s.work:
		case <-s.done:
			return
		}
		progressed := e.t.step()

		s.mu.Lock()
		e.running = false
		if e.removed {
			s.mu.Unlock()
			e.t.close()
			continue
		}
		switch {
		case e.t.more:
			e.interval = 0 // backlog: come back right after other due files
		case progressed || e.interval == 0:
			e.interval = s.poll
		default:
			e.interval = min(e.interval*2, s.idlePoll)
		}
		e.next = s.now().Add(e.interval)
		heap.Push(&s.queue, e)
		s.mu.Unlock()
		s.signal()
	}
}

// stop halts polling, gives every file a final read (so lines written just
// before shutdown are processed now) and closes them. It returns the tailers.
func (s *scheduler) stop() []*tailer {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return nil
	}
	s.stopped = true
	s.mu.Unlock()
	close(s.done)
	s.wg.Wait()

	s.mu.Lock()
	ts := make([]*tailer, 0, len(s.entries))
	for t := range s.entries {
		ts = append(ts, t)
	}
	s.mu.Unlock()
	for _, t := range ts {
		for t.step() && t.more {
		}
		t.close()
	}
	return ts
}

// entryHeap orders entries by next poll time.
type entryHeap []*entry

func (h entryHeap) Len() int           { return len(h) }
func (h entryHeap) Less(i, j int) bool { return h[i].next.Before(h[j].next) }
func (h entryHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i]; h[i].index = i; h[j].index = j }
func (h *entryHeap) Push(x any)        { e := x.(*entry); e.index = len(*h); *h = append(*h, e) }
func (h *entryHeap) Pop() any {
	old := *h
	e := old[len(old)-1]
	old[len(old)-1] = nil
	*h = old[:len(old)-1]
	e.index = -1
	return e
}
