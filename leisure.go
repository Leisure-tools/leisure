package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aki237/nscjar"
	"github.com/alecthomas/kong"
	"github.com/leisure-tools/server"
)

const (
	DEFAULT_UNIX_SOCKET = ".leisure.socket"
	DEFAULT_PORT        = 7315
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

//go:embed html/*
var html embed.FS

type Overlay struct {
	stack []fs.FS
}

func (ofs *Overlay) Open(name string) (file fs.File, err error) {
	for i := len(ofs.stack) - 1; i >= 0; i-- {
		file, err = ofs.stack[i].Open(name)
		if err == nil {
			return
		}
	}
	return
}

func (ofs *Overlay) Add(name string) error {
	if info, err := os.Stat(name); err != nil {
		if os.IsNotExist(err) {
			panic("File " + name + " does not exist")
		}
		panic("Could not open dirctory " + name)
	} else if !info.IsDir() {
		panic("Not a dirctory: " + name)
	}
	ofs.stack = append(ofs.stack, os.DirFS(name))
	return nil
}

var htmlDirs = make([]fs.FS, 0, 4)

func (cli *CLI) verbose(level int, format string, args ...any) {
	if level <= cli.globals.Verbose {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	}
}

func concat[T any](array []T, values ...T) []T {
	return append(append(make([]T, 0, len(array)+len(values)), array...), values...)
}

func (cli *CLI) lockName() string {
	cli.verbose(1, "LOCK, COOKIES: %s", cli.globals.Cookies)
	if cookieFile, err := filepath.Abs(cli.globals.Cookies); err != nil {
		panic(err)
	} else {
		if real, err := filepath.EvalSymlinks(cli.globals.Cookies); err == nil {
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

func (cli *CLI) checkLock(lockName string) (bool, string, int) {
	if cli.globals.Parent == 0 {
		cli.globals.Parent = os.Getppid()
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
			if !cli.globals.Lock {
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
			} else if userObj.Username == luser && hostname == lhost && cli.globals.Parent == lpid {
				// it's our file
				return true, "", 0
			} else if !cli.globals.ForceLock {
				// it's not our file and we're not forcing the lock
				return false, luser, lpid
			}
		}
		// create the lock file, smashing over the old one if it exists
		target := fmt.Sprintf("%s@%s.%d:%d", userObj.Username, hostname, cli.globals.Parent, time.Now().Unix())
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

type Cmd interface {
	run(globals *GlobalOpts)
}

func (cli *CLI) check() {
	if cli.globals.Cookies != "" {
		cli.verbose(1, "Using cookie file %s", cli.globals.Cookies)
		if cli.globals.ForceLock {
			cli.globals.Lock = true
		}
		owned, user, pid := cli.checkLock(cli.lockName())
		if !owned && cli.globals.Lock {
			e := server.NewLeisureError(ErrLocked.Type, "user", user, "pid", pid)
			panicWith("%w: the cookies file %s is already locked by user %s, process %d", e, cli.globals.Cookies, user, pid)
		}
	}
}

func (cmd *PeerCmd) Run(cli *CLI) error {
	cli.verbose(1, "PEER")
	if html, err := fs.Sub(html, "html"); err != nil {
		panic(err)
	} else {
		cmd.ofs = &Overlay{append(make([]fs.FS, 0, 2), html)}
	}
	mux := http.NewServeMux()
	sv := server.Initialize(cmd.UnixSocket, mux, server.MemoryStorage)
	//if opts.localFiles != "" {
	//	opts.ofs.Add(opts.localFiles)
	//}
	mux.Handle("/", http.FileServer(http.FS(cmd.ofs)))
	sv.SetVerbose(cmd.Verbose)
	//fmt.Fprintln(os.Stderr, "Leisure", strings.Join(args, " "))
	var listener *net.UnixListener
	die = func() {
		if listener != nil {
			listener.Close()
		}
		os.Exit(exitCode)
	}
	if cmd.Port != 0 {
		go http.ListenAndServe(fmt.Sprintf("localhost:%d", cmd.Port), mux)
	}
	cli.verbose(1, "UNIX SOCKET: %s", cmd.UnixSocket)
	if addr, err := net.ResolveUnixAddr("unix", cmd.UnixSocket); err != nil {
		panicWith("%w: could not resolve unix socket %s", ErrSocketFailure, cmd.UnixSocket)
	} else if listener, err = net.ListenUnix("unix", addr); err != nil {
		panicWith("%w: could not listen on unix socket %s", ErrSocketFailure, cmd.UnixSocket)
	} else {
		listener.SetUnlinkOnClose(true)
		cli.verbose(1, "RUNNING UNIX DOMAIN SERVER: %s", addr)
		log.Fatal(http.Serve(listener, &myMux{mux}))
	}
	return nil
}

type myMux struct {
	*http.ServeMux
}

func (mux *myMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawPath != "" {
		r.URL.Path = r.URL.RawPath
	}
	mux.ServeMux.ServeHTTP(w, r)
}

func (cli *CLI) httpClient() *http.Client {
	if cli.globals.Host != "" {
		cli.verbose(1, "USING HOST: %s:%i", cli.globals.Host, cli.globals.Port)
		return &http.Client{
			Transport: &http.Transport{
				DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("tcp", fmt.Sprint(cli.globals.Host, ":", cli.globals.Port))
				},
			},
		}
	}
	cli.verbose(1, "USING UNIX SOCKET: '%s'", cli.globals.UnixSocket)
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", cli.globals.UnixSocket)
			},
		},
	}
}

func (cli *CLI) request(method string, body io.Reader, urlStr string, moreUrl ...string) *http.Response {
	for i, str := range moreUrl {
		moreUrl[i] = url.PathEscape(str)
	}
	hostname := cli.globals.Host
	if hostname == "" {
		hostname = "leisure"
	}
	if cli.globals.Port != 0 {
		hostname += fmt.Sprint(":", cli.globals.Port)
	}
	uri := fmt.Sprint("http://", hostname)
	if path, err := url.JoinPath(urlStr, moreUrl...); err != nil {
		cli.globals.ctx.PrintUsage(true)
		return nil
	} else if req, err := http.NewRequest(method, uri+path, body); err != nil {
		panic(err)
	} else {
		req.URL = &url.URL{
			Scheme: req.URL.Scheme,
			Host:   req.URL.Host,
			Opaque: path,
		}
		cli.verbose(1, "%s %s", method, req.URL.String())
		if body != nil {
			req.Header.Set("Content-Type", "text/plain")
		}
		cli.verbose(1, "Cookies: %s", cli.globals.Cookies)
		if cli.globals.Cookies != "" {
			jar := nscjar.Parser{}
			if file, err := os.Open(cli.globals.Cookies); err == nil {
				if cookies, err := jar.Unmarshal(file); err != nil {
					panicWith("%w: could not read cookie file %s", ErrCookieFailure, cli.globals.Cookies)
				} else {
					for _, cookie := range cookies {
						req.AddCookie(cookie)
					}
				}
			}
		}
		client := cli.httpClient()
		if resp, err := client.Do(req); err != nil {
			panic(err)
		} else {
			if resp.StatusCode == http.StatusOK && cli.globals.Cookies != "" {
				jar := nscjar.Parser{}
				if file, err := os.Create(cli.globals.Cookies); err != nil {
					panicWith("%w: could not write cookie file %s", ErrCookieFailure, cli.globals.Cookies)
				} else {
					cli.verbose(1, "Cookies:%v", resp.Cookies())
					for _, cookie := range resp.Cookies() {
						if err := jar.Marshal(file, cookie); err != nil {
							panicWith("%w: could not write cookie file %s", ErrCookieFailure, cli.globals.Cookies)
						}
					}
				}
			}
			return resp
		}
	}
}

func (cli *CLI) get(url string, components ...string) *http.Response {
	return cli.request("GET", nil, url, components...)
}

func (cli *CLI) post(url string, body io.Reader) *http.Response {
	return cli.request("POST", body, url)
}

func (cli *CLI) postOrGet(url string) *http.Response {
	if body, err := io.ReadAll(os.Stdin); err != nil {
		panicWith("%w: error reading input", ErrBadCommand)
		return nil
	} else if len(body) > 0 {
		return cli.post(url, bytes.NewReader(body))
	} else {
		return cli.get(url)
	}
}

func output(resp *http.Response) {
	outputBasic(resp, false)
}

func outputBasic(resp *http.Response, parseJson bool) {
	var obj any
	var buf []byte
	var err error
	if resp.StatusCode != http.StatusOK {
		exitCode = resp.StatusCode
		buf, err = io.ReadAll(resp.Body)
		if err != nil {
			panic(err)
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
	if !parseJson {
		io.Copy(os.Stdout, resp.Body)
		return
	}
	buf, err = io.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}
	err = json.Unmarshal(buf, &obj)
	if err != nil {
		panic(err)
	}
	fmt.Print(obj)
}

func (cmd *StopCmd) Run(cli *CLI) error {
	cli.globals.UnixSocket = cmd.UnixSocket
	cli.globals.Port = cmd.Port
	cli.globals.Host = cmd.Host
	cli.globals.Verbose = cmd.Verbose
	cli.get(server.STOP)
	return nil
}

func (cmd *DocCreateCmd) Run(cli *CLI) error {
	if cmd.DocId == "" {
		key := make([]byte, 16)
		rand.Read(key)
		cmd.DocId = hex.EncodeToString(key)
	}
	url := server.DOC_CREATE + cmd.DocId
	if cmd.Alias != "" {
		url += "?alias=" + cmd.Alias
	}
	cli.post(url, os.Stdin)
	return nil
}

func (cmd *DocListCmd) Run(cli *CLI) error {
	output(cli.get(server.DOC_LIST))
	return nil
}

func (cmd *DocGetCmd) Run(cli *CLI) error {
	args := make([]string, 0, 14)
	args = append(args, server.DOC_GET, cmd.DocId)
	initial := len(args)
	addQuery := func(key, value string) {
		prefix := "&"
		if len(args) == initial {
			prefix = "?"
		}
		args = append(args, prefix, key, "=", value)
	}
	if cmd.Hash != "" {
		addQuery("hash", cmd.Hash)
	}
	if cmd.Dump {
		addQuery("dump", "true")
	}
	if cmd.Org {
		addQuery("org", "true")
	}
	if cmd.Data {
		addQuery("data", "true")
	}
	outputBasic(cli.get(strings.Join(args, "")), true)
	return nil
}

func (cmd *SessionUnlockCmd) Run(cli *CLI) error {
	result := false
	if cli.globals.Cookies != "" {
		name := cli.lockName()
		owner, user, pid := cli.checkLock(name)
		if !owner && !cli.globals.ForceLock {
			panicWith("%w: lock file is owned by user %s, pid %d",
				server.NewLeisureError(ErrUnlocking.Type, "filename", name), user, pid)
		} else if _, err := os.Lstat(name); err != nil {
			panicWith("%w: could not stat lock file %s, %s",
				server.NewLeisureError(ErrUnlocking.Type, "filename", name), name, err.Error())
		} else if err := os.Remove(name); err != nil {
			panicWith("%w: could not remove lock file %s, %s",
				server.NewLeisureError(ErrUnlocking.Type, "filename", name), name, err.Error())
		}
		result = true
	}
	fmt.Println(result)
	return nil
}

func (opts *DocConnectionArgs) query() []string {
	query := make([]string, 0, 3)
	if opts.Org {
		query = append(query, "org=true")
	}
	if opts.Data {
		query = append(query, "dataOnly=true")
	}
	if opts.NoStrings {
		query = append(query, "strings=false")
	}
	return query
}

// TODO support input for a POST instead of a GET
func (cmd *SessionCreateCmd) Run(cli *CLI) error {
	query := cmd.query()
	if len(query) > 0 {
		output(cli.get(server.SESSION_CREATE + cmd.Session + "/" + cmd.DocId + "?" + strings.Join(query, "&")))
	} else {
		output(cli.get(server.SESSION_CREATE, cmd.Session, cmd.DocId))
	}
	return nil
}

func (cmd *SessionListCmd) Run(cli *CLI) error {
	output(cli.get(server.SESSION_LIST))
	return nil
}

func (cmd *SessionDocCmd) Run(cli *CLI) error {
	output(cli.get(server.SESSION_DOCUMENT))
	return nil
}

func (cmd *SessionGetCmd) Run(cli *CLI) error {
	output(cli.get(server.SESSION_GET, cmd.Name))
	return nil
}

func (cmd *SessionSetCmd) Run(cli *CLI) error {
	if cmd.Name != "" {
		if url, err := url.JoinPath(server.SESSION_SET, cmd.Name); err == nil {
			output(cli.post(url, os.Stdin))
		} else {
			fmt.Fprintf(os.Stderr, "Error: %v", err)
			cli.globals.ctx.PrintUsage(false)
		}
	} else {
		output(cli.post(server.SESSION_SET, os.Stdin))
	}
	return nil
}

func panicWith(format string, args ...any) {
	panic(server.ErrorJSON(fmt.Errorf(format, args...)))
}

func (cmd *SessionConnectCmd) Run(cli *CLI) error {
	url := server.SESSION_CONNECT + cmd.Session
	q := ""
	query := cmd.query()
	if cmd.DocId != "" {
		query = append(query, "doc="+cmd.DocId)
	}
	if cmd.Force {
		query = append(query, "force=true")
	}
	if len(query) > 0 {
		q = "?" + strings.Join(query, "&")
	}
	cli.verbose(1, "sending session connect: %s", url+q)
	output(cli.postOrGet(url + q))
	return nil
}

func (cmd *SessionEditCmd) Run(cli *CLI) error {
	output(cli.post(server.SESSION_EDIT, os.Stdin))
	return nil
}

func (cmd *SessionRefreshCmd) Run(cli *CLI) error {
	output(cli.post(server.SESSION_EDIT, strings.NewReader(`{
		"selectionOffset":0,
		"selectionLength":0,
		"replacements": []
	}`)))
	return nil
}

func (cmd *SessionUpdateCmd) Run(cli *CLI) error {
	//fmt.Fprintln(os.Stderr, "TIMEOUT: ", cmd.Timeout)
	output(cli.get(server.SESSION_UPDATE + fmt.Sprintf("?timeout=%d", cmd.Timeout)))
	return nil
}

func (cmd *SessionTagCmd) Run(cli *CLI) error {
	output(cli.get(server.SESSION_TAG, cmd.Name))
	return nil
}

func (cmd *ParseCmd) Run(cli *CLI) error {
	output(cli.post(server.ORG_PARSE, os.Stdin))
	return nil
}

func (cmd *GetCmd) get(cli *CLI) error {
	output(cli.get(cmd.URL))
	return nil
}

func main() {
	cli := CLI{}
	initGlobalOpts(&cli)
	ctx := kong.Parse(&cli)
	cli.verbose(1, "MAIN")
	cli.globals.ctx = ctx
	cli.check()
	cli.defaults()
	cli.verbose(1, "RUN")
	ctx.Run(&cli)
}
