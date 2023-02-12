package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"

	"github.com/leisure-tools/server"
)

func usage() {
	fmt.Fprintln(os.Stderr, `Usage: leisure PATH [-l PORT | -]

Run Leisure server on UNIX domain socket at PATH, optionally listening on PORT.

PATH must not exist, it is created when Leisure is run and removed when it is finished.

if -l is given, Leisure will listen on a TCP socket at PORT, if port is 0, a random port
will be chosen and printed to standard out.`)
}

func main() {
	if len(os.Args) == 1 || len(os.Args[1]) == 0 || os.Args[1][0] == '-' {
		fmt.Println("ARGS:", os.Args)
		usage()
	}
	socket := os.Args[1]
	flags := &flag.FlagSet{}
	port := flags.Int("l", -1, "TCP port to listen on")
	flags.Parse(os.Args[2:])
	mux := http.NewServeMux()
	server.Initialize(socket, mux, server.MemoryStorage)
	fmt.Println("Leisure", strings.Join(os.Args[1:], " "))
	var listener *net.UnixListener
	exitCode := 0
	die := func() {
		if listener != nil {
			listener.Close()
		}
		os.Exit(exitCode)
	}
	sigs := make(chan os.Signal)
	signal.Notify(sigs, os.Interrupt, os.Kill)
	go func() {
		<-sigs
		fmt.Println("Dying from signal")
		exitCode = 1
		die()
	}()
	defer func() {
		if err := recover(); err != nil {
			exitCode = 1
		}
		die()
	}()
	if *port != -1 {
		go http.ListenAndServe(fmt.Sprintf("localhost:%d", *port), mux)
	}
	if addr, err := net.ResolveUnixAddr("unix", socket); err != nil {
		panic("Could not get unix socket " + socket)
	} else if listener, err = net.ListenUnix("unix", addr); err != nil {
		panic("Could not listen on unix socket " + socket)
	} else {
		fmt.Println("SETTING UNLINK")
		listener.SetUnlinkOnClose(true)
		log.Fatal(http.Serve(listener, mux))
	}
}
