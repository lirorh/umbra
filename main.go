package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

var logger *log.Logger = log.New(os.Stdout, "", log.Ltime)

func main() {
	var (
		listen  string
		backend string
		shadow  string
	)
	flags := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	flags.StringVar(&listen, "listen", "", "Listen for connections on this address.")
	flags.StringVar(&backend, "backend", "", "The address of the backend to forward to.")
	flags.StringVar(&shadow, "shadow", "", "The address of the backend to forward to.")
	flags.Parse(os.Args[1:])

	if listen == "" || backend == "" || shadow == "" {
		fmt.Fprintln(os.Stderr, "listen, remote and shadow options required")
		os.Exit(1)
	}

	p := Proxy{
		Listen:  listen,
		Backend: backend,
		Shadow:  shadow,
	}

	sigs := make(chan os.Signal)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		logger.Printf("Shutting down proxy")
		if err := p.Close(); err != nil {
			logger.Fatal(err.Error())
		}
	}()

	if err := p.Run(); err != nil {
		logger.Fatal(err.Error())
	}
}

// Copy data between three connections. Return EOF on connection close.
func Pipe(local, backend, shadow net.Conn) error {
	done := make(chan error, 1)

	proxy := func(from, to, shadow net.Conn) {
		var n int64
		var err error
		logger.Printf("Starting copying")
		n, err = io.Copy(ShadowWriter(to, shadow), from)
		logger.Printf("copied %d bytes from %s to %s", n, from.RemoteAddr(), to.RemoteAddr())
		done <- err
	}

	respond := func(from, to net.Conn) {
		n, err := io.Copy(to, from)
		logger.Printf("copied %d bytes from %s to %s", n, from.RemoteAddr(), to.RemoteAddr())
		done <- err
	}

	go proxy(local, backend, shadow)
	go respond(backend, local)

	err1 := <-done
	err2 := <-done
	if err1 != nil {
		return err1
	}
	if err2 != nil {
		return err2
	}
	return nil
}

// Proxy connections from Listen to Backend.
type Proxy struct {
	Listen   string
	Backend  string
	Shadow   string
	listener net.Listener
}

func (p *Proxy) Run() error {
	var err error
	if p.listener, err = net.Listen("tcp", p.Listen); err != nil {
		return err
	}

	wg := &sync.WaitGroup{}
	for {
		if conn, err := p.listener.Accept(); err == nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				p.handle(conn)
			}()
		} else {
			return nil
		}
	}
	wg.Wait()
	return nil
}

func (p *Proxy) Close() error {
	return p.listener.Close()
}

func (p *Proxy) handle(upConn net.Conn) {
	defer upConn.Close()
	logger.Printf("accepted: %s", upConn.RemoteAddr())
	backendConn, err := net.Dial("tcp", p.Backend)
	if err != nil {
		logger.Printf("unable to connect to backend %s: %s", p.Backend, err)
		return
	}
	defer func() {
		backendConn.Close()
	}()

	shadowConn, err := net.Dial("tcp", p.Shadow)
	if err != nil {
		logger.Printf("unable to connect to shadow %s: %s", p.Shadow, err)
	} else {
		defer func() {
			shadowConn.Close()
		}()
	}

	if err := Pipe(upConn, backendConn, shadowConn); err != nil {
		logger.Printf("pipe failed: %s", err)
	} else {
		logger.Printf("disconnected: %s", upConn.RemoteAddr())
	}
}

type shadowWriter struct {
	writers []io.Writer
}

func (t *shadowWriter) Write(p []byte) (n int, err error) {
	// first, write to the backend
	n, err = t.writers[0].Write(p)
	if err != nil {
		return
	}
	if n != len(p) {
		err = io.ErrShortWrite
		return
	}
	// now write to the shadow, ignore errors
	if t.writers[1] != nil {
		t.writers[1].Write(p)
	}
	return len(p), nil
}

// MultiWriter creates a writer that duplicates its writes to all the
// provided writers, similar to the Unix tee(1) command.
func ShadowWriter(backend, shadow io.Writer) io.Writer {
	w := make([]io.Writer, 0)
	w = append(w, backend)
	w = append(w, shadow)
	return &shadowWriter{w}
}
