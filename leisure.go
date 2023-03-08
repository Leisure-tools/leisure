package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"os/user"
	"path"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aki237/nscjar"
	"github.com/leisure-tools/server"
)

const (
	DEFAULT_UNIX_SOCKET = ".leisure.socket"
	FILES_PATH          = "/files/"
)

var ErrSocketFailure = server.NewLeisureError("socketFailure")
var ErrBadCommand = server.NewLeisureError("badCommand")
var ErrCookieFailure = server.NewLeisureError("cookieFailure")
var ErrLocking = server.NewLeisureError("errorLocking")
var ErrLocked = server.NewLeisureError("alreadyLocked")
var ErrUnlocking = server.NewLeisureError("errorUnlocking")

var exitCode = 0
var die = func() {
	os.Exit(exitCode)
}

//go:embed all:html
var html embed.FS

func (opts *options) usage(err bool) {
	cmds := make([]string, 0, len(opts.cmds))
	for key := range opts.cmds {
		cmds = append(cmds, key)
	}
	sort.Strings(cmds)
	fmt.Fprint(os.Stderr, `Usage: leisure COMMAND OPTIONS

COMMANDS:

`)
	for _, cmd := range cmds {
		opts.cmds[cmd].baseUsage("")
	}
	if err {
		os.Exit(1)
	}
	os.Exit(0)
}

type options struct {
	cmds           commands
	globalFlags    *flag.FlagSet
	host           string
	unixSocket     string
	cookieFile     string
	tcpPort        int
	docId          string
	docAlias       string
	docHash        string
	lockCookies    bool
	forceLock      bool
	force          bool
	ppid           int
	verbosity      verboser
	wantsOrg       bool
	wantsNoStrings bool
	dataOnly       bool
	localFiles     string
}

type verboser struct{ level int }

type command struct {
	fn             commandFunc
	leaf           bool
	flags          *flag.FlagSet
	name           []string
	description    string
	argDescription string
	session        bool
}

type commandFunc = func(cmd *command, opts *options, args []string)

type commands map[string]*command

func (opts *options) addGlobalOpts(fl *flag.FlagSet) {
	fl.StringVar(&opts.unixSocket, "unixsocket", ".leisure.socket", "`PATH` to unix socket -- PATH will be created and must not exist beforehand")
	fl.Var(&opts.verbosity, "v", "verbose")
}

func (v *verboser) String() string { return "" }

func (v *verboser) Set(value string) error {
	v.level++
	return nil
}

func (v *verboser) IsBoolFlag() bool { return true }

func (opts *options) verbose(level int, format string, args ...any) {
	if level <= opts.verbosity.level {
		fmt.Fprintf(os.Stderr, format, args...)
	}
}

func (opts *options) initCommands() {
	cmd := opts.add("peer", (*command).peer, "", "Run a leisure peer on unix domain socket PATH and, optionally, on a TCP port.")
	cmd.flags.StringVar(&opts.localFiles, "html", "", "`DIRECTORY` to serve files from")
	cmd.flags.IntVar(&opts.tcpPort, "l", -1, "TCP `PORT` to listen on")
	cmd = opts.add("doc list", (*command).docList, "", "List all documents.")
	cmd = opts.add("doc create", (*command).docCreate, "", "Create a document.")
	cmd.flags.StringVar(&opts.docId, "id", "", "`ID` of document")
	cmd.flags.StringVar(&opts.docAlias, "alias", "", "`ALIAS` for document")
	cmd = opts.add("doc get", (*command).docGet, "DOCID | ALIAS", "Get a document.")
	cmd.flags.StringVar(&opts.docHash, "hash", "", "`HASH` for document")
	cmd = opts.add("session list", (*command).sessionList, "", "List all sessions.")
	cmd = opts.addSession("create", (*command).sessionCreate, "SESSIONID DOCID | ALIAS", "Create a session.")
	cmd.flags.BoolVar(&opts.wantsOrg, "org", false, "receive org changes")
	cmd.flags.BoolVar(&opts.wantsNoStrings, "nostrings", false, "receive org changes")
	cmd.flags.BoolVar(&opts.dataOnly, "data", false, "receive only data changes")
	cmd = opts.addSession("get", (*command).sessionGet, "", "Get a session's document.")
	cmd = opts.addSession("connect", (*command).sessionConnect, "SESSIONID", "Connect to a session.")
	cmd.flags.StringVar(&opts.docId, "doc", "", "`ID or ALIA` of document")
	cmd.flags.BoolVar(&opts.wantsOrg, "org", false, "receive org changes")
	cmd.flags.BoolVar(&opts.wantsNoStrings, "nostrings", false, "receive org changes")
	cmd.flags.BoolVar(&opts.dataOnly, "data", false, "receive only data changes")
	cmd.flags.BoolVar(&opts.force, "force", false, "force connection, even if session is in use")
	cmd = opts.addSession("edit", (*command).sessionEdit, "", "Add edits to a session.")
	cmd = opts.addSession("update", (*command).sessionUpdate, "", "Check if a session has updates.")
	cmd = opts.addSession("unlock", (*command).sessionUnlock, "", "Unlock session.")
	cmd = opts.add("parse", (*command).parse, "", "parse an org document. Example: leisure get /default.org | leisure parse")
	cmd = opts.add("get", (*command).get, "", "HTTP get request to leisure server")
}

func concat[T any](array []T, values ...T) []T {
	return append(append(make([]T, 0, len(array)+len(values)), array...), values...)
}

func (opts *options) add(cmdName string, fn commandFunc, args, description string) *command {
	prefix := strings.Split(cmdName, " ")
	cmds := make([]command, len(prefix))
	for i := range prefix {
		cmd := &cmds[i]
		cmd.flags = &flag.FlagSet{}
		cmd.name = prefix[:i+1]
		opts.cmds[strings.Join(cmd.name, " ")] = cmd
		if i == len(prefix)-1 {
			cmd.fn = fn
			cmd.leaf = true
			cmd.description = description
			cmd.argDescription = args
			opts.addGlobalOpts(cmd.flags)
		} else {
			keyPrefix := strings.Join(cmd.name, " ") + " "
			cmd.fn = func(cmd *command, opts *options, args []string) {
				if len(args) == 0 {
					cmds[0].commandSetUsage(opts)
				}
				if cmd := opts.cmds[keyPrefix+args[0]]; cmd == nil {
					panicWith("%w: unrecognized command: %s", ErrBadCommand, os.Args[1])
				} else {
					fmt.Fprintf(os.Stderr, "command: %v\n", cmd)
					cmd.call(opts, args[1:])
				}
			}
		}
	}
	return &cmds[len(cmds)-1]
}

// create a session command and add the -cookies opt to it
func (opts *options) addSession(cmdName string, fn commandFunc, args, description string) *command {
	cmd := opts.add("session "+cmdName, fn, args, description)
	cmd.session = true
	cmd.flags.StringVar(&opts.cookieFile, "cookies", "", "`PATH` to cookies file")
	cmd.flags.BoolVar(&opts.lockCookies, "lock", false, "lock the cookies file")
	cmd.flags.BoolVar(&opts.forceLock, "forcelock", false, "lock the cookies file, removing other locks")
	cmd.flags.IntVar(&opts.ppid, "parent", 0, "process using leisure, in case the parent is not the real owner")
	return cmd
}

func newOptions() *options {
	opts := &options{
		host:       "http://leisure", // assume unix socket
		unixSocket: ".leisure.socket",
		cmds:       commands{},
	}
	opts.initCommands()
	return opts
}

func (cmd *command) commandSetUsage(opts *options) {
	fmt.Fprintf(os.Stderr, "Usage: %s SUBCOMMAND OPTIONS\n", cmd.name[0])
	names := make([]string, 0, len(opts.cmds))
	prefix := cmd.name[0] + " "
	for name := range opts.cmds {
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		opts.cmds[name].baseUsage("")
	}
	os.Exit(1)
}

func (cmd *command) usage() {
	cmd.baseUsage("Usage: ")
	os.Exit(1)
}

func (cmd *command) baseUsage(prefix string) {
	if cmd.leaf {
		fmt.Fprintf(os.Stderr, "%s%s %s  -- %s\n\n", prefix, strings.Join(cmd.name, " "), cmd.argDescription, cmd.description)
		cmd.flags.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
}

func (opts *options) lockName() string {
	if cookieFile, err := filepath.Abs(opts.cookieFile); err != nil {
		panic(err)
	} else {
		if real, err := filepath.EvalSymlinks(opts.cookieFile); err == nil {
			cookieFile = real
		}
		parent, name := path.Split(cookieFile)
		return path.Join(parent, fmt.Sprintf(".#%s", name))
	}
}

func eatTo(str, delim string) (string, string) {
	d := strings.Index(str, delim)
	return str[:d], str[d+1:]
}

func (opts *options) checkLock(lockName string) (bool, string, int) {
	if opts.ppid == 0 {
		opts.ppid = os.Getppid()
	}
	if userObj, err := user.Current(); err != nil {
		panicWith("%w: %s", ErrLocking, err)
	} else if hostname, err := os.Hostname(); err != nil {
		panicWith("%w: %s", ErrLocking, err)
	} else {
		if _, err := os.Lstat(lockName); err != nil {
			if os.IsExist(err) {
				// couldn't stat it but it does exist
				panicWith("%w: %s", ErrLocking, err)
			}
			// doesn't exist
			if !opts.lockCookies {
				return true, "", 0
			}
			//fall through to creating the link
		} else if dest, err := os.Readlink(lockName); err != nil {
			// maybe it's a file or a directory instead of a link
			panicWith("%w: %s", ErrLocking, err)
		} else {
			// the file exists, parse the link target
			luser, dest := eatTo(dest, "@")
			lhost, dest := eatTo(dest, ".")
			lpidStr, dest := eatTo(dest, ":")
			if lpid, err := strconv.Atoi(lpidStr); err != nil {
				panicWith("%w: %s", ErrLocking, err)
			} else if userObj.Username == luser && hostname == lhost && opts.ppid == lpid {
				// it's our file
				return true, "", 0
			} else if !opts.forceLock {
				// it's not our file and we're not forcing the lock
				return false, luser, lpid
			}
		}
		// create the lock file, smashing over the old one if it exists
		target := fmt.Sprintf("%s@%s.%d:%d", userObj.Username, hostname, opts.ppid, time.Now().Unix())
		parent := path.Dir(lockName)
		for i := 0; i < 5; i++ {
			buf := make([]byte, 8)
			if _, err := rand.Read(buf[:]); err != nil {
				panicWith("%w: %s", ErrLocking, err)
			}
			tmp := path.Join(parent, hex.EncodeToString(buf))
			if err := os.Symlink(target, tmp); err == nil {
				if err := os.Rename(tmp, lockName); err != nil {
					// could not overwrite the lock
					panicWith("%w: %s", ErrLocking, err)
				}
				return true, "", 0
			}
		}
	}
	panicWith("%w: could not create lock file", ErrLocking)
	return false, "", 0
}

func (cmd *command) call(opts *options, args []string) {
	if err := cmd.flags.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		cmd.usage()
	}
	if cmd.session && opts.cookieFile != "" {
		opts.verbose(1, "Using cookie file %s\n", opts.cookieFile)
		if opts.forceLock {
			opts.lockCookies = true
		}
		owned, user, pid := opts.checkLock(opts.lockName())
		if !owned && opts.lockCookies {
			e := server.NewLeisureError(ErrLocked.Type, "user", user, "pid", pid)
			panicWith("%w: the cookies file %s is already locked by user %s, process %d", e, opts.cookieFile, user, pid)
		}
	}
	cmd.fn(cmd, opts, cmd.flags.Args())
}

func (cmd *command) argCount(count int, args []string) {
	if len(args) != count {
		cmd.usage()
	}
}

func (cmd *command) peer(opts *options, args []string) {
	opts.unixSocket = DEFAULT_UNIX_SOCKET
	if len(args) > 0 {
		opts.unixSocket = args[0]
	}
	htmlFiles, err := fs.Sub(html, "html")
	if err != nil {
		panic(err)
	}
	mux := http.NewServeMux()
	sv := server.Initialize(opts.unixSocket, mux, server.MemoryStorage)
	if opts.localFiles != "" {
		mux.Handle("/", http.FileServer(http.Dir(opts.localFiles)))
	} else {
		mux.Handle("/", http.FileServer(http.FS(htmlFiles)))
	}
	sv.SetVerbose(opts.verbosity.level)
	fmt.Println("Leisure", strings.Join(args, " "))
	var listener *net.UnixListener
	die = func() {
		if listener != nil {
			listener.Close()
		}
		os.Exit(exitCode)
	}
	if opts.tcpPort != -1 {
		go http.ListenAndServe(fmt.Sprintf("localhost:%d", opts.tcpPort), mux)
	}
	if addr, err := net.ResolveUnixAddr("unix", opts.unixSocket); err != nil {
		panicWith("%w: could not resolve unix socket %s", ErrSocketFailure, opts.unixSocket)
	} else if listener, err = net.ListenUnix("unix", addr); err != nil {
		panicWith("%w: could not listen on unix socket %s", ErrSocketFailure, opts.unixSocket)
	} else {
		listener.SetUnlinkOnClose(true)
		fmt.Printf("RUNNING UNIX DOMAIN SERVER: %+v\n", addr)
		log.Fatal(http.Serve(listener, mux))
	}
}

func (opts *options) unixClient(path string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", opts.unixSocket)
			},
		},
	}
}

func (opts *options) request(method string, body io.Reader, urlStr string, moreUrl ...string) *http.Response {
	if url, err := url.JoinPath(urlStr, moreUrl...); err != nil {
		opts.usage(true)
		return nil
	} else if req, err := http.NewRequest(method, opts.host+url, body); err != nil {
		panic(err)
	} else {
		opts.verbose(1, "%s %s\n", method, url)
		if body != nil {
			req.Header.Set("Content-Type", "text/plain")
		}
		if opts.cookieFile != "" {
			jar := nscjar.Parser{}
			if file, err := os.Open(opts.cookieFile); err == nil {
				if cookies, err := jar.Unmarshal(file); err != nil {
					panicWith("%w: could not read cookie file %s", ErrCookieFailure, opts.cookieFile)
				} else {
					for _, cookie := range cookies {
						req.AddCookie(cookie)
					}
				}
			}
		}
		client := opts.unixClient(opts.unixSocket)
		if resp, err := client.Do(req); err != nil {
			panic(err)
		} else {
			if resp.StatusCode == http.StatusOK && opts.cookieFile != "" {
				jar := nscjar.Parser{}
				if file, err := os.Create(opts.cookieFile); err != nil {
					panicWith("%w: could not write cookie file %s", ErrCookieFailure, opts.cookieFile)
				} else {
					opts.verbose(1, "Cookies:%v\n", resp.Cookies())
					for _, cookie := range resp.Cookies() {
						if err := jar.Marshal(file, cookie); err != nil {
							panicWith("%w: could not write cookie file %s", ErrCookieFailure, opts.cookieFile)
						}
					}
				}
			}
			return resp
		}
	}
}

func (opts *options) get(url string, components ...string) *http.Response {
	return opts.request("GET", nil, url, components...)
}

func (opts *options) post(url string, body io.Reader) *http.Response {
	return opts.request("POST", body, url)
}

func (opts *options) postOrGet(url string) *http.Response {
	if body, err := io.ReadAll(os.Stdin); err != nil {
		panicWith("%w: error reading input", ErrBadCommand)
		return nil
	} else if len(body) > 0 {
		return opts.post(url, bytes.NewReader(body))
	} else {
		return opts.get(url)
	}
}

func output(resp *http.Response) {
	if resp.StatusCode != http.StatusOK {
		exitCode = resp.StatusCode
		var obj any
		var buf []byte
		var err error
		buf, err = io.ReadAll(resp.Body)
		if err != nil {
			return
		} else if err := json.Unmarshal(buf, &obj); err == nil {
			if errObj, ok := obj.(map[string]any); ok && errObj["error"] != "" {
				errObj["args"] = os.Args
				if j, jerr := json.Marshal(errObj); jerr == nil {
					fmt.Print(string(j))
					return
				}
			}
		}
		fmt.Print(string(buf))
		return
	}
	io.Copy(os.Stdout, resp.Body)
}

func (cmd *command) docCreate(opts *options, args []string) {
	if opts.docId == "" {
		key := make([]byte, 16)
		rand.Read(key)
		opts.docId = hex.EncodeToString(key)
	}
	url := server.DOC_CREATE + opts.docId
	if opts.docAlias != "" {
		url += "?alias=" + opts.docAlias
	}
	opts.post(url, os.Stdin)
}

func (cmd *command) docList(opts *options, args []string) {
	cmd.argCount(0, args)
	output(opts.get(server.DOC_LIST))
}

func (cmd *command) docGet(opts *options, args []string) {
	cmd.argCount(1, args)
	if opts.docHash != "" {
		output(opts.get(server.DOC_GET, args[0], "?", "hash", opts.docHash))
		return
	}
	output(opts.get(server.DOC_GET, args[0]))
}

func (cmd *command) sessionUnlock(opts *options, args []string) {
	cmd.argCount(0, args)
	if opts.cookieFile != "" {
		name := opts.lockName()
		owner, user, pid := opts.checkLock(name)
		if !owner && !opts.forceLock {
			panicWith("%w: lock file is owned by user %s, pid %d",
				server.NewLeisureError(ErrUnlocking.Type, "filename", name), user, pid)
		} else if _, err := os.Lstat(name); err != nil {
			panicWith("%w: could not stat lock file %s, %s",
				server.NewLeisureError(ErrUnlocking.Type, "filename", name), name, err.Error())
		} else if err := os.Remove(name); err != nil {
			panicWith("%w: could not remove lock file %s, %s",
				server.NewLeisureError(ErrUnlocking.Type, "filename", name), name, err.Error())
		} else {
			fmt.Printf("true")
		}
	} else {
		fmt.Printf("false")
	}
}

func (cmd *command) sessionCreate(opts *options, args []string) {
	cmd.argCount(2, args)
	output(opts.get(server.SESSION_CREATE, args[0], args[1]))
}

func (cmd *command) sessionList(opts *options, args []string) {
	cmd.argCount(0, args)
	output(opts.get(server.SESSION_LIST))
}

func (cmd *command) sessionGet(opts *options, args []string) {
	cmd.argCount(0, args)

	output(opts.get(server.SESSION_GET))
}

func panicWith(format string, args ...any) {
	panic(server.ErrorJSON(fmt.Errorf(format, args...)))
}

func (cmd *command) sessionConnect(opts *options, args []string) {
	cmd.argCount(1, args)
	url := server.SESSION_CONNECT + args[0]
	q := ""
	query := make([]string, 0, 3)
	if opts.docId != "" {
		query = append(query, "doc="+opts.docId)
	}
	if opts.wantsOrg {
		query = append(query, "org=true")
	}
	if opts.dataOnly {
		query = append(query, "dataOnly=true")
	}
	if opts.wantsNoStrings {
		query = append(query, "strings=false")
	}
	if opts.force {
		query = append(query, "force=true")
	}
	if len(query) > 0 {
		q = "?" + strings.Join(query, "&")
	}
	fmt.Fprintln(os.Stderr, "sending session connect:", url+q)
	output(opts.postOrGet(url + q))
}

func (cmd *command) sessionEdit(opts *options, args []string) {
	output(opts.post(server.SESSION_EDIT, os.Stdin))
}

func (cmd *command) sessionUpdate(opts *options, args []string) {
	output(opts.get(server.SESSION_UPDATE))
}

func (cmd *command) parse(opts *options, args []string) {
	output(opts.post(server.ORG_PARSE, os.Stdin))
}

func (cmd *command) get(opts *options, args []string) {
	cmd.argCount(1, args)
	output(opts.get(args[0]))
}

func main() {
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
			fmt.Fprintln(os.Stderr, "DYING FROM PANIC:", err)
			debug.PrintStack()
			fmt.Println(server.ErrorJSON(err))
			exitCode = 1
		}
		die()
	}()
	opts := newOptions()
	if len(os.Args) == 1 || len(os.Args[1]) == 0 || os.Args[1][0] == '-' {
		fmt.Println("ARGS:", os.Args)
		opts.usage(false)
	}
	cmd := opts.cmds[os.Args[1]]
	if cmd == nil {
		panicWith("%w: unrecognized command: %s", ErrBadCommand, os.Args[1])
	}
	cmd.call(opts, os.Args[2:])
}
