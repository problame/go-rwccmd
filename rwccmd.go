// rwcccmd provides rwccmd.Cmd, which starts a new process and exposes its stdout & stdin
// as a combined io.ReadWriteCloser.
package rwccmd

import (
	"syscall"
	"os/exec"
	"io"
	"sync/atomic"
	"runtime"
	"time"
	"context"
	"errors"
	"sync"
)

const (
	st_init int32 = iota
	st_close_begin
	st_close_end
)

// Cmd represents a spawned process for its entire lifecycle.
// Cmd is not concurrency-safe (Read and Write) must be serialized, but cancellation
// can happen asynchronously through the context's CancelFunc, the Close method or the CloseAtDeadline method.
//
// After the process was killed due to context.CancelFunc, Close or CloseAtDeadline, I/O operations return ErrDeadline.
//
// If it stops executing due to a signal from outside this application, I/O operations return an ErrKilledFromOutside.
//
// If it exists by its own, I/O operations return io.EOF if it exists with status 0 (success),
// and io.ErrUnexpectedEOF in all other exists (non-signal) cases.
type Cmd struct {
	state int32 // state
	// only valid if state == st_close_end
	waitErr error
	// only valid if state == st_close_end
	ioErr error
	// only valid if state == st_close_end
	waitStatus syscall.WaitStatus

	dlt *time.Timer
	dltMtx sync.Mutex

	cmd *exec.Cmd
	cancel context.CancelFunc
	stdout io.ReadCloser
	stdin io.WriteCloser

	log Logger
}

// CommandContext creates a command with a given context, which allows for cancellation / deadlines and for providing
// a logger via the ContextWithLog function.
//
// The process must be started using the Start() method.
func CommandContext(ctx context.Context, bin string, args []string, env []string) (*Cmd, error) {

	ctx, cancel := context.WithCancel(ctx)
	log := contextLog(ctx)

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env

	log.Printf("create stdout pipe")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	log.Printf("create stdin pipe")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	c := &Cmd{cmd: cmd, cancel: cancel, stdout: stdout, stdin:stdin, log: log}
	atomic.StoreInt32(&c.state, st_init)

	return c, nil
}

func (c *Cmd) waitForCloseEnd() {
	closed := atomic.LoadInt32(&c.state) == st_close_end
	if !closed {
		c.log.Printf("wait for close to finish")
	}
	for ;atomic.LoadInt32(&c.state) != st_close_end; {
		runtime.Gosched()
	}
	if !closed {
		c.log.Printf("close finished")
	}
}

func (c *Cmd) Pid() int {
	// FIXME: race with Wait()
	return c.cmd.Process.Pid
}

func (c *Cmd) Kill() error {
	// FIXME: race with Wait()
	return c.cmd.Process.Signal(syscall.SIGKILL)
}

func (c *Cmd) WaitStatus() (ws syscall.WaitStatus, ok bool) {
	if atomic.LoadInt32(&c.state) != st_close_end {
		return syscall.WaitStatus(0), false
	}
	return c.waitStatus, true
}

func (c *Cmd) Start() error {
	return c.cmd.Start()
}

func (c *Cmd) Read(out []byte) (n int, err error) {
	if atomic.LoadInt32(&c.state) != st_init {
		c.waitForCloseEnd()
		return 0, c.ioErr
	}
	n, err = c.stdout.Read(out)
	if err != nil {
		c.log.Printf("Read() error: %s", err)
		err = c.close(errorWhileRW) // this Read() revealed the error, will return the root cause waitErr
	}
	return n, err
}

var errorWhileRW = errors.New("errorWhileRW")

var ErrDeadline = errors.New("deadline expired")
var ErrKilledFromOutside = errors.New("process killed from outside the application")

func (c *Cmd) Write(out []byte) (n int, err error) {
	if atomic.LoadInt32(&c.state) != st_init {
		c.waitForCloseEnd()
		return 0, c.ioErr
	}
	n, err = c.stdin.Write(out)
	if err != nil {
		c.log.Printf("Write() error: %s", err)
		err = c.close(errorWhileRW) // this Write() revealed the error, will return the root cause waitErr
	}
	return n, err
}

// CloseAtDeadline sets a timer to close the process at a specific time in in the future.
// A zero value for deadline stops the timer.
//
// Note on net.Conn deadlines:
//
// Closing the 'connection' when missing a deadline does not comply with the specified behavior of net.Conn,
// hence it is incorrect to mimic a net.Conn with *rwcccmd.Cmd
//
// The reason why this is functionality cannot be provided is that *os.File does not provide a way to cancel
// a Read & Write by other means than calling Close, which may not fit all use cases.
func (c *Cmd) CloseAtDeadline(deadline time.Time) error {
	if atomic.LoadInt32(&c.state) != st_init {
		c.waitForCloseEnd()
		return c.ioErr
	}

	c.dltMtx.Lock()
	defer c.dltMtx.Unlock()

	if c.dlt != nil {
		if !c.dlt.Stop() {
			return errors.New("CloseAtDeadline while timer expires")
		}
		c.dlt = nil
	}

	if deadline.IsZero() {
		return nil
	}

	c.dlt = time.AfterFunc(deadline.Sub(time.Now()), func() {
		c.close(ErrDeadline)
	})
	return nil
}

func (c *Cmd) Close() (err error) {
	if atomic.LoadInt32(&c.state) != st_init {
		c.waitForCloseEnd()
		return c.ioErr
	}
	return c.close(io.EOF)
}

func (c *Cmd) close(ioErr error) (err error) {

	c.log.Printf("call to close() with ioErr = %s", ioErr)

	if !atomic.CompareAndSwapInt32(&c.state, st_init, st_close_begin) {
		// we have a competing closer, and it won
		c.waitForCloseEnd()
		// after c.waitForClosEnd() returns, use the ioErr from the competing closer
		// since we want to mimick this call to be like any other 2nd, 3rd, etc
		return c.ioErr
	}
	defer atomic.StoreInt32(&c.state, st_close_end)

	c.log.Printf("send cancel")
	c.cancel()
	c.log.Printf("wait for process to exit")
	waitErr := c.cmd.Wait()
	if waitErr == nil {
		c.log.Printf("we have not cancelled the process, it has exited with exit status 0")
		c.ioErr = io.EOF
		c.waitErr = io.EOF
		return c.waitErr
	}

	exitErr, ok := waitErr.(*exec.ExitError)
	if !ok {
		// os/exec documents this as I/O errors
		c.log.Printf("generic wait error, probably I/o: %s", waitErr)
		c.waitErr = waitErr
		c.ioErr = waitErr
		return waitErr
	}

	c.log.Printf("wait returned ExitError: %s", exitErr)
	ws := exitErr.Sys().(syscall.WaitStatus)
	c.waitStatus = ws
	c.log.Printf("WaitStatus: exited=%v signaled=%v exitStatus=%v signal='%v'",
		ws.Exited(), ws.Signaled(), ws.ExitStatus(), ws.Signal())

	// errorWhileRW is only set if Read() or Write() fail in the middle of PipeIO
	// This happens only when
	//   (1) an asynchronous action calls close() --- some code inside this app closed it
	//   (2) the process has been killed with SIGKILL by another process on the machine (not us) and we didn't notice it
	// Because (1) is never called with errorWhileRW and (2) is only noticed at Read() or Write(),
	// we can use errorWhileRW to tell the difference between (1) and (2)
	weKilledIt := ws.Signaled() && ws.Signal() == syscall.SIGKILL && ioErr != errorWhileRW
	if weKilledIt {
		if ioErr == errorWhileRW {
			panic("must not call from from other places than Read() or Write() with errorWhileRW")
		}
		c.log.Printf("we killed the process")
		c.waitErr = nil
		c.ioErr = ioErr
		return
	}
	c.log.Printf("we didn't kill the process")

	if ws.Exited() {
		c.log.Printf("we have not cancelled the process, it exited with non-zero exit status %d", ws.ExitStatus())
		c.waitErr = io.ErrUnexpectedEOF
		c.ioErr = io.ErrUnexpectedEOF
		return c.ioErr
	}

	c.waitErr = ErrKilledFromOutside
	c.ioErr = ErrKilledFromOutside
	return c.waitErr
}
