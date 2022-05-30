package job

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"reflect"
	"time"

	"golang.org/x/exp/slices"
)

// feeder records logs from an input channel and feeds them to many output
// channels. Outfeeds can be attached at any time, and they will be fed
// the logs from the start of recording. If the outfeed is not following
// the logs, it will be closed once all the recorded logs have been fed.
// If the outfeed is following then it will continue to receive logs as
// long as there is an infeed. If the infeed is closed, all followers
// become non-followers and will be closed when they reach the end of
// the recorded logs.
type feeder struct {
	control  chan outfeed
	infeed   <-chan Log
	outfeeds []*outfeed
	cases    []reflect.SelectCase
	buffer   []Log
	// outOffset is the number of select cases before the first
	// outfeed in the cases slice.
	outOffset    int
	infeedClosed bool
}

type Log struct {
	Timestamp time.Time
	Line      []byte
}

type outfeed struct {
	ch     chan<- Log
	done   <-chan struct{}
	pos    int
	follow bool
}

func newFeeder(infeed <-chan Log) *feeder {
	control := make(chan outfeed)
	f := feeder{
		infeed:  infeed,
		control: control,
		cases: []reflect.SelectCase{
			{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(control)},
			{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(infeed)},
		},
	}
	return &f
}

func (f *feeder) attachOutfeed(follow bool, done <-chan struct{}) <-chan Log {
	ch := make(chan Log)
	feed := outfeed{
		ch:     ch,
		done:   done,
		follow: follow,
	}
	f.control <- feed
	return ch
}

// Start runs the loop of the feeder. It will run until the done channel is
// closed, which happens when the job this feeder is attached to is cleaned
// up. Until then, it is always possible to get a feed of the recorded logs,
// even if the job has long since terminated.
func (f *feeder) Start(done <-chan struct{}) {
	doneCase := reflect.SelectCase{
		Dir:  reflect.SelectRecv,
		Chan: reflect.ValueOf(done),
	}
	f.cases = append(f.cases, doneCase)
	f.outOffset = len(f.cases) // offset of first outfeed in select cases slice

	disabled := reflect.Value{}

	for {
		i, rcv, ok := reflect.Select(f.cases)
		isOutfeed := i >= f.outOffset && (i-f.outOffset)%2 == 0
		isOutfeedDone := i >= f.outOffset && (i-f.outOffset)%2 == 1
		feedIdx := (i - f.outOffset) / 2
		switch {
		case i == 0 && ok: // control
			outfeed := rcv.Interface().(outfeed)
			f.addOutfeed(&outfeed)
		case i == 1 && ok: // infeed
			l := rcv.Interface().(Log)
			f.buffer = append(f.buffer, l)
			f.wakeSleepers()
		case i == 1 && !ok: // infeed closed
			f.infeedClosed = true
			f.cases[1].Chan = disabled
			f.removeSleepers()
		case i == 2: // done
			for _, feed := range f.outfeeds {
				close(feed.ch)
			}
			return
		case isOutfeed:
			feed := f.outfeeds[feedIdx]
			feed.pos++
			if feed.pos < len(f.buffer) {
				// Set up the feed for its next line
				f.cases[i].Send = reflect.ValueOf(f.buffer[feed.pos])
			} else if feed.follow && !f.infeedClosed {
				// Disable send channel until more logs come in
				f.cases[i].Chan = disabled
			} else {
				// not following and we have reached the end of the
				// buffer for this feed. Close and remove the feed.
				f.removeOutfeed(feedIdx)
			}
		case isOutfeedDone:
			f.removeOutfeed(feedIdx)
		}
	}
}

func (f *feeder) addOutfeed(feed *outfeed) {
	// If feed start position is past the end of the buffer and it is not
	// following, close the channel and return
	if feed.pos >= len(f.buffer) && (!feed.follow || f.infeedClosed) {
		close(feed.ch)
		return
	}

	f.outfeeds = append(f.outfeeds, feed)

	c := reflect.SelectCase{Dir: reflect.SelectSend}
	if feed.pos < len(f.buffer) {
		c.Chan = reflect.ValueOf(feed.ch)
		c.Send = reflect.ValueOf(f.buffer[feed.pos])
	}
	f.cases = append(f.cases, c)

	c = reflect.SelectCase{
		Dir:  reflect.SelectRecv,
		Chan: reflect.ValueOf(feed.done),
	}
	f.cases = append(f.cases, c)
}

func (f *feeder) wakeSleepers() {
	disabled := reflect.Value{}
	for i, feed := range f.outfeeds {
		caseIdx := i*2 + f.outOffset
		if f.cases[caseIdx].Chan == disabled && feed.pos < len(f.buffer) {
			f.cases[caseIdx].Chan = reflect.ValueOf(feed.ch)
			f.cases[caseIdx].Send = reflect.ValueOf(f.buffer[feed.pos])
		}
	}
}

// Remove any sleepers, as the infeed has closed and there will be no more
// logs. This terminates followers when the input stream closes.
func (f *feeder) removeSleepers() {
	disabled := reflect.Value{}
	newfeeds := make([]*outfeed, 0, len(f.outfeeds))
	newcases := make([]reflect.SelectCase, 0, len(f.cases))
	newcases = append(newcases, f.cases[0:f.outOffset]...)
	for i, feed := range f.outfeeds {
		caseIdx := i*2 + f.outOffset
		if f.cases[caseIdx].Chan == disabled {
			close(feed.ch)
			continue
		}
		// Keep enabled feeds
		newfeeds = append(newfeeds, f.outfeeds[i])
		newcases = append(newcases, f.cases[caseIdx])
		newcases = append(newcases, f.cases[caseIdx+1])
	}
	f.outfeeds = newfeeds
	f.cases = newcases
}

func (f *feeder) removeOutfeed(i int) {
	close(f.outfeeds[i].ch)
	f.outfeeds = slices.Delete(f.outfeeds, i, i+1)
	caseIdx := i*2 + f.outOffset
	f.cases = slices.Delete(f.cases, caseIdx, caseIdx+2)
}

func infeed(r io.Reader, out chan<- Log) {
	// XXX Unfortunately this is unlikely to work to put a maximum size on
	// the read. This just sets the minimum size of the buffer, but it could
	// potentially grow. We will probably need to do our own chunking of
	// the data read. Still to do.
	const maxLineSize = 512
	buf := bufio.NewReaderSize(r, maxLineSize)

	// The infeed loop terminates when the Reader r returns an error or
	// EOF. This occurs when the process attached to that reader exits
	// (either naturally or by being killed).
	// XXX This will need a different way to terminate the loop if we
	// want to be able to shutdown the jobber server but keep the jobs
	// running, and perhaps somehow re-attach to them later. This is
	// way way way out of scope :)
	for {
		line, err := buf.ReadBytes('\n')
		if len(line) > 0 {
			out <- Log{Timestamp: time.Now(), Line: line}
		}
		if err != nil && err != bufio.ErrBufferFull && err != io.EOF {
			// XXX Should log, but no logger yet
			fmt.Fprintf(os.Stderr, "unexpected error on job output: %v", err)
		}
		if err != nil && err != bufio.ErrBufferFull {
			break
		}
	}
	close(out)
}
