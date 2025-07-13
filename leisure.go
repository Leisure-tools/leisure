package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aki237/nscjar"
	"github.com/alecthomas/kong"
	"github.com/leisure-tools/history"
	"github.com/leisure-tools/monitor"
	"github.com/leisure-tools/org"
	"github.com/leisure-tools/server"
	u "github.com/leisure-tools/utils"
	"gopkg.in/yaml.v2"
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
var HEADER_PROPS = []string{"type", "topic", "targets", "updateTopics", "updateTargets", "tags", "root", "quiet", "return", "code"}
var exitCode = 0
var die = func() {
	os.Exit(exitCode)
}

var MONITOR_TYPES = u.NewSet("monitor", "data", "code", "delete")

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

func verbose(reqLevel, level int, format string, args ...any) {
	if reqLevel <= level {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	}
}

func (cli *CLI) verbose(level int, format string, args ...any) {
	verbose(level, cli.globals.Verbose, format, args...)
}

func (l *leisure) verbose(level int, format string, args ...any) {
	verbose(level, l.Verbose, format, args...)
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
	inst := &leisure{
		LeisureService: sv,
		Monitors:       make(map[string]*docMonitor),
	}
	if m := monitor.MON_PAT.FindStringSubmatch(cmd.Monitor); m != nil {
		inUser := m[monitor.MON_PAT.SubexpIndex("user")]
		inPass := m[monitor.MON_PAT.SubexpIndex("pass")]
		inHost := m[monitor.MON_PAT.SubexpIndex("host")]
		inPort := m[monitor.MON_PAT.SubexpIndex("port")]
		inDb := m[monitor.MON_PAT.SubexpIndex("db")]
		port, tlsPort, pass, tlsConfig, err := monitor.ReadRedisConf(cmd.MonitorConf)
		if err != nil {
			panic(err)
		}
		if pass != "" {
			inPass = pass
		}
		if tlsPort != 0 {
			port = tlsPort
		}
		if port != 0 {
			inPort = fmt.Sprint(port)
		}
		if inHost == "" {
			inHost = "localhost"
		}
		if inPort == "" {
			inPort = "6739"
		}
		if inUser != "" || inPass != "" {
			inHost = "@" + inHost
		}
		if inPass != "" {
			inHost = ":" + inPass + inHost
		}
		if inUser != "" {
			inHost = inUser + inHost
		}
		conStr := fmt.Sprintf("redis://%s:%s/%s", inHost, inPort, inDb)
		inst.initMonitor(mux, conStr, tlsConfig, cmd.Verbose)
	}
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

type leisure struct {
	*server.LeisureService
	*monitor.Monitoring
	Monitors map[string]*docMonitor
}

type lcontext struct {
	*server.LeisureContext
}

// only supports exclusive LeisureSessions for now
type docMonitor struct {
	*leisure
	*monitor.RemoteMonitor
	*server.LeisureSession
	lastUpdate   int64
	blockSerials map[org.OrgId]string
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

func (cli *CLI) postOrGet(url string) (*http.Response, bool) {
	if body, err := io.ReadAll(os.Stdin); err != nil {
		panicWith("%w: error reading input", ErrBadCommand)
		return nil, false
	} else if len(body) > 0 {
		return cli.post(url, bytes.NewReader(body)), true
	} else {
		return cli.get(url), false
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
	output(cli.post(url, os.Stdin))
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
	resp, post := cli.postOrGet(url + q)
	if post || resp.StatusCode != http.StatusOK {
		output(resp)
	} else {
		var obj any
		if buf, err := io.ReadAll(resp.Body); err != nil {
			panic(err)
		} else if err = json.Unmarshal(buf, &obj); err != nil {
			panic(err)
		} else if errObj, ok := obj.(map[string]any); ok && errObj["error"] != "" {
			errObj["args"] = os.Args
			if j, jerr := json.Marshal(errObj); jerr == nil {
				fmt.Printf(string(j))
				return nil
			}
		}
		fmt.Print(obj)
	}
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

func (l *leisure) initMonitor(mux *http.ServeMux, monStr string, tlsConf *tls.Config, verbose int) {
	m, err := monitor.New(monStr, verbose, tlsConf)
	if err != nil {
		panic(err)
	}
	l.Monitoring = m
	m.InitMux(mux)
	l.AddListener(l)
}

func writeBlock(w io.Writer, m map[string]any) error {
	enc := yaml.NewEncoder(w)
	fmt.Fprintln(w, "#+NAME: ", m["name"])
	m2 := map[string]any{}
	maps.Copy(m2, m)
	delete(m2, "name")
	lang := "yaml"
	if m["type"] == "code" {
		_, hasLanguage := m["language"]
		if l, ok := m["language"].(string); !hasLanguage || !ok {
			return fmt.Errorf("Language is not a string: %v", m)
		} else {
			lang = l
			delete(m2, "language")
		}
	}
	fmt.Fprint(w, "#+BEGIN_SRC ", lang)
	for _, prop := range []string{"type", "origin", "root", "topics", "tags", "targets", "updateTopics", "updateTargets", "quiet", "topic"} {
		if v, ok := m2[prop]; ok {
			fmt.Fprint(w, " :", prop)
			switch o := v.(type) {
			case string:
				fmt.Fprint(w, " ", v)
			case []string:
				for _, s := range o {
					fmt.Fprint(w, " ", s)
				}
			case []any:
				for _, s := range o {
					fmt.Fprint(w, " ", s)
				}
			default:
				return fmt.Errorf("Unsupported type for property %s: %v", prop, v)
			}
			delete(m2, prop)
		}
	}
	fmt.Fprintln(w)
	delete(m2, "value")
	if len(m2) > 0 {
		return fmt.Errorf("Unsupported properties for block: %v", m2)
	} else if m["type"] == "code" {
		v, _ := m["value"]
		if s, ok := v.(string); !ok {
			return fmt.Errorf("Bad value for code block: %v", m)
		} else {
			if strings.HasPrefix(s, "#+") || strings.Contains(s, "\n#+") {
				fmt.Fprint(w, ",")
				for _, c := range s {
					if c != '\n' {
						fmt.Fprint(w, c)
					} else {
						fmt.Fprint(w, ',', c)
					}
				}
			} else {
				fmt.Fprintln(w, s)
			}
		}
	} else if _, ok := m["value"]; !ok {
		return fmt.Errorf("Bad value for %s block: %v", m["type"], m)
	} else if err := enc.Encode(m["value"]); err != nil {
		return err
	}
	fmt.Fprintln(w, "#+END_SRC")
	return nil
}

// if leisure is monitoring, make a "MONITOR-"+ID session for each new document
func (l *leisure) NewDocument(sv *server.LeisureService, id string) {
	if l.Monitoring == nil {
		return
	} else if rm, err := l.Monitoring.Add(id); err != nil {
		panic(err)
	} else {
		l.verbose(1, "CREATED DOCUMENT, MONITORING SESSION MONITOR-"+id)
		name := ""
		for alias, docId := range sv.DocumentAliases {
			if docId == id {
				name = alias
				break
			}
		}
		wantsOrg := strings.HasSuffix(name, ".org")
		updates, err := sv.AddSession("MONITOR-"+id, sv.Documents[id], wantsOrg, false, false, 0)
		if err != nil {
			panic(err)
		}
		if wantsOrg {
			l.verbose(1, "NEW DOCUMENT: "+name+" MONITOR-"+id)
			updates.ExclusiveDoc = updates.GetLatestDocument()
			updates.Chunks = org.Parse(updates.ExclusiveDoc.String())
			updates.ExternalFmt = writeBlock
			updates.ExternalBlocks = rm.Changed
		}
		updates.Connect()
		dm := &docMonitor{
			leisure:        l,
			RemoteMonitor:  rm,
			LeisureSession: updates,
			lastUpdate:     0,
			blockSerials:   make(map[org.OrgId]string),
		}
		rm.AddListener(dm)
		updates.AddListener(dm)
		l.Monitors[id] = dm
		//updates.History.AddListener(dm)
		blocks := make([]map[string]any, 0, updates.Chunks.Chunks.Measure().Count)
		count := 0
		for ch := range updates.Chunks.Iter() {
			l.verbose(1, "BLOCK %#v", ch)
			count++
			if bl := dm.dataBlockFor(org.ChunkRef{Chunk: ch, OrgChunks: updates.Chunks}); bl != nil {
				blocks = append(blocks, bl)
			}
		}
		if len(blocks) > 0 {
			l.verbose(1, "SENDING %d BLOCKS", len(blocks))
			dm.BasicPatch(true, false, blocks...)
		} else {
			l.verbose(1, "NO BLOCKS FOUND OUT OF %d", count)
		}
	}
}

// the document changed
func (dm *docMonitor) NewHeads(s *history.History) {
	// find changed heads
	heads := s.Heads()
	latest := dm.LeisureSession.LatestBlock()
	sessionHeads := u.NewSet(latest.Parents...)
	sessionHeads.Add(latest.Hash)
	if sessionHeads.Has(heads...) {
		// no changed heads
		return
	}
	// check harder -- look at block order and see if any occur before latest
	o := dm.GetBlockOrder()
	recent := o[len(o)-1]
	latestHash := latest.Hash
	if recent == latestHash {
		return
	}
	dm.updateSession()
}

func (dm *docMonitor) DataChanged(rm *monitor.RemoteMonitor) {
	if dm.ExclusiveDoc != nil {
		dm.Service.Svc(func() {
			dm.HasUpdate = true
		})
		go func() { dm.Updates <- true }()
	} else {
		sharedDataChanged(dm, rm)
	}
}

func (dm *docMonitor) DocumentChanged(s *server.LeisureSession, ch *org.ChunkChanges) {
	dm.verbose(1, "@@@ RECEIVED DOCUMENT CHANGED @@@")
	blockNames := make(u.Set[org.OrgId], len(ch.Added)+len(ch.Changed)+len(ch.Removed))
	blocks := make([]map[string]any, 0, len(ch.Added)+len(ch.Changed)+len(ch.Removed))
	for _, name := range ch.Removed {
		if blk, exists := dm.Blocks[string(name)]; exists {
			blockNames.Add(name)
			blocks = append(blocks, map[string]any{
				"type":   "delete",
				"name":   name,
				"topics": blk["topics"],
				"value":  name,
			})
		}
	}
	for name := range u.JoinIters(u.SetIterable(ch.Added), u.SetIterable(ch.Changed)) {
		if blockNames.Has(name) {
			continue
		}
		chunk := s.ChunkRef(name)
		if src, isSrc := chunk.Chunk.(*org.SourceBlock); isSrc {
			if sends := src.GetOption("send"); sends != nil {
				send := strings.Join(sends, " ")
				old, has := dm.blockSerials[name]
				if !has || old != send {
					dm.verbose(1, "sending change, old serial: %v, new serial: %v", old, send)
					blockNames.Add(name)
					blocks = append(blocks, dm.dataBlockFor(s.ChunkRef(name)))
					dm.blockSerials[name] = send
				} else {
					dm.verbose(1, "ignoring change, old serial: %v, new serial: %v\n  opts: %v\n  send: %v\n  block: %#v", old, send, src.GetOptions(), src.GetOption("send"), src)
				}
			}
		}
	}
	if len(blocks) > 0 {
		dm.verbose(1, "@@@  SEND: %v", blocks)
		dm.BasicPatch(true, false, blocks...)
	}
}

func (dm *docMonitor) updateSession() {
	// merge the session doc, check changes, send to REDIS
	if changes, err := dm.SessionEdit([]history.Replacement{}, -1, -1); err != nil {
		panic(err)
	} else {
		dm.updateSessionFromChanges(changes)
	}
}

func (dm *docMonitor) updateSessionFromChanges(changes map[string]any) {
	oldOrg := dm.LeisureSession.Chunks.Chunks
	added, _ := changes["added"].([]org.ChunkRef)
	changed, _ := changes["changed"].([]org.ChunkRef)
	added = append(added, changed...)
	removed, _ := changes["removed"].([]org.OrgId)
	if len(added)+len(removed) > 0 {
		patch := make([]map[string]any, 0, len(added)+len(removed))
		for _, ch := range added {
			if block := dm.dataBlockFor(ch); block != nil {
				patch = append(patch, block)
			}
		}
		// items were removed from the document
		// remove any corresponding monitoring blocks
		for _, id := range removed {
			if left, ch := org.GetChunk(id, oldOrg); !left.IsEmpty() {
				if name := org.Name(ch); name != "" && dm.Blocks[name] != nil {
					delete(dm.Blocks, name)
					patch = append(patch, map[string]any{
						"type":  "delete",
						"name":  name,
						"topic": dm.DefaultTopic,
					})
				}
			}
		}
		dm.BasicPatch(true, false, patch...)
	}
}

func (dm *docMonitor) dataBlockFor(chunk org.ChunkRef) map[string]any {
	name := org.Name(chunk.Chunk)
	if name == "" {
		dm.verbose(1, "NO NAME FOR BLOCK %#v", chunk.Chunk)
		return nil
	}
	var opts map[string]string
	block := map[string]any{"name": name}
	var sblock *org.SourceBlock
	switch oblk := chunk.Chunk.(type) {
	case *org.TableBlock:
		block["value"] = oblk.Value
		opts = oblk.GetInheritedOptions(chunk.OrgChunks, "", "")
		if !MONITOR_TYPES.Has(opts["type"]) {
			opts["type"] = "data"
		}
	case *org.SourceBlock:
		sblock = oblk
		block["value"] = oblk.Value
		if oblk.Value == nil {
			lead := "\n  "
			dm.verbose(1, "NO VALUE FOR BLOCK:%s%s", lead, strings.ReplaceAll(oblk.Text, "\n", lead))
		}
		opts = oblk.GetFullOptions(chunk.OrgChunks)
		dm.verbose(1, "SOURCE BLOCK OPTIONS: %#v", opts)
	default:
		return nil
	}
	switch opts["type"] {
	case "monitor":
		copyOpts(opts, block, "type", "origin", "topics", "tags", "targets", "updateTopics", "root", "quiet")
	case "data":
		copyOpts(opts, block, "type", "topics", "tags", "targets", "code")
	case "code":
		copyOpts(opts, block, "type", "topics", "tags", "targets", "language", "return", "updateTopics")
		block["value"] = sblock.Text[sblock.Content:sblock.End]
	case "delete":
		if dm.Blocks[name] == nil {
			return nil
		}
		delete(dm.Blocks, name)
		copyOpts(opts, block, "type", "topics", "targets", "value")
	default:
		dm.verbose(1, "Unknown block type, %v", opts["type"])
		return nil
	}
	dm.verbose(1, "\nFOUND BLOCK %s: %#v\n", name, block)
	return block
}

func copyOpts(opts map[string]string, block map[string]any, props ...string) {
	for _, prop := range props {
		block[prop] = opts[prop]
	}
}

func sharedDataChanged(dm *docMonitor, rm *monitor.RemoteMonitor) {
	dm.verbose(1, "PROCESSING CHANGED DATA FOR SHARED SESSION: %s", dm.SessionId)
	activity := ""
	defer func() {
		if rerr := recover(); rerr != nil {
			err, ok := rerr.(error)
			if !ok {
				err = fmt.Errorf("%v", rerr)
			}
			fmt.Fprintf(os.Stderr, "Error %s: %v\n", activity, err)
			debug.PrintStack()
		}
	}()
	// update doc first
	activity = "updating doc"
	var trackChunks org.ChunkChanges
	if _, _, _, err := dm.Commit(0, 0, &trackChunks); err != nil {
		panic(err)
	}
	// find changes in doc
	serial, changes, deletes := rm.GetUpdates(dm.lastUpdate, 0, false)
	dm.lastUpdate = serial
	if len(changes)+len(deletes) == 0 {
		return
	}
	new := u.NewSet[string]()
	names := make(map[org.OrgId]string)
	pos := make(map[org.OrgId]int)
	chunks := make([]org.ChunkRef, 0, len(changes)+len(deletes))
	for name := range changes {
		loc, ch := dm.Chunks.LocateChunkNamed(name)
		if ch.IsEmpty() {
			new.Add(name)
		} else {
			names[ch.AsOrgChunk().Id] = name
			pos[ch.AsOrgChunk().Id] = loc
			chunks = append(chunks, ch)
		}
	}
	for _, name := range deletes {
		loc, ch := dm.Chunks.LocateChunkNamed(name)
		pos[ch.AsOrgChunk().Id] = loc
		chunks = append(chunks, ch)
	}
	// reverse sort changes
	sort.Slice(chunks, func(i, j int) bool {
		return pos[chunks[i].AsOrgChunk().Id] > pos[chunks[j].AsOrgChunk().Id]
	})
	lc := &lcontext{
		LeisureContext: &server.LeisureContext{
			LeisureService: dm.LeisureService,
			Session:        dm.LeisureSession,
		},
	}
	activity = "adding data"
	// add new chunks to doc
	for name := range new {
		if _, err := lc.AddData(name, changes[name]); err != nil {
			panic(err)
		}
	}
	// make replacements in doc
	for _, chunk := range chunks {
		ch := chunk.Chunk
		start := 0
		end := 0
		switch block := ch.(type) {
		case *org.TableBlock:
			start = block.TblStart
			end = len(block.Text)
		case *org.SourceBlock:
			start = block.SrcStart
			end = block.End
		}
		id := ch.AsOrgChunk().Id
		if data, ok := changes[names[id]]; ok {
			activity = "setting data"
			if _, err := lc.SetData(pos[id], start, end, ch, server.JsonV(data)); err != nil {
				panic(err)
			}
		} else {
			activity = "removing data"
			lc.Session.Replace(pos[id], len(ch.AsOrgChunk().Text), "")
		}
	}
	var trackChanges org.ChunkChanges
	activity = "committing changes"
	if _, _, _, err := dm.LeisureSession.Commit(0, 0, &trackChanges); err != nil {
		panic(err)
	}
}

func (lc *lcontext) AddData(name string, val any) (map[string]any, error) {
	if block, ok := val.(map[string]any); !ok {
		return nil, fmt.Errorf("map but got %#v", block)
	} else if origType, ok := block["type"].(string); !ok {
		return nil, fmt.Errorf("expected type in block %#v", block)
	} else {
		sb := strings.Builder{}
		endsInNL := false
		doclen := lc.Session.Chunks.Chunks.Measure().Width
		if doclen != 0 {
			lastText := lc.Session.Chunks.Chunks.PeekLast().AsOrgChunk().Text
			endsInNL = len(lastText) > 0 && lastText[len(lastText)-1] == '\n'
		}
		text := ""
		language := "yaml"
		headers := headerProps(block, HEADER_PROPS...)
		if !endsInNL {
			sb.WriteRune('\n')
		}
		delete(block, "name")
		if origType == "code" {
			if str, ok := block["value"].(string); !ok {
				return nil, fmt.Errorf("bad value string for code block: %#v", block["value"])
			} else {
				text = str
			}
			if lang, ok := block["language"].(string); ok {
				language = lang
			} else {
				language = "julia"
			}
			delete(block, "language")
		} else if bytes, err := yaml.Marshal(block["value"]); err != nil {
			return nil, err
		} else {
			text = string(bytes)
		}
		fmt.Fprintf(&sb, `#+name: %s\n`, name)
		printBlock(&sb, language, headers, text)
		return lc.ReplaceText(-1, -1, doclen, 0, sb.String(), false)
	}
}

func printBlock(io io.Writer, language string, headers []string, body string) {
	fmt.Fprintf(io, `#+begin_src %s`, language)
	if headers != nil {
		for _, str := range headers {
			fmt.Fprint(io, " ", str)
		}
	}
	fmt.Fprintf(io, `\n%s\n#+end_src\n`, strings.TrimSpace(body))
}

func (lc *lcontext) SetData(offset, start, end int, cur org.Chunk, val any) (map[string]any, error) {
	sb := strings.Builder{}
	if block, ok := val.(map[string]any); !ok {
		return nil, fmt.Errorf("expected map but got %#v", block)
	} else if origType, ok := block["type"].(string); !ok {
		return nil, fmt.Errorf("expected type in block %#v", block)
	} else if tbl, ok := cur.(*org.TableBlock); ok && !tableApropos(block) {
		return nil, fmt.Errorf("only data can be stored in a table but block is %#v", block)
	} else if codeValue, ok := block["value"].(string); !ok && origType == "code" {
		return nil, fmt.Errorf("code blocks expect strings for values but block is %#v", block)
	} else if tbl != nil {
		for _, row := range block["value"].([]any) {
			for _, cell := range row.([]any) {
				fmt.Fprint(&sb, "| ", cell)
			}
			fmt.Fprint(&sb, " |\n")
		}
	} else {
		language := "yaml"
		origLang := block["language"]
		headers := headerProps(block, HEADER_PROPS...)
		text := ""
		if origType == "code" {
			if lang, ok := origLang.(string); ok {
				language = lang
			} else {
				language = "julia"
			}
			text = codeValue
		} else if bytes, err := yaml.Marshal(block["value"]); err != nil {
			return nil, err
		} else {
			text = string(bytes)
		}
		printBlock(&sb, language, headers, text)
	}
	return lc.ReplaceText(-1, -1, start, end-start, sb.String(), false)
}

func tableApropos(block map[string]any) bool {
	if block["type"] != "data" {
		return false
	}
	for prop := range block {
		if prop == "type" || prop == "value" {
			continue
		}
		return false
	}
	if tbl, ok := block["value"].([]any); !ok {
		return false
	} else {
		for _, row := range tbl {
			if _, ok := row.([]any); !ok {
				return false
			}
		}
	}
	return true
}

func headerProps(value map[string]any, names ...string) []string {
	result := make([]string, 0, len(names)*2)
	for _, name := range names {
		if str, ok := value[name].(string); ok && str != "" {
			result = append(result, name, str)
			delete(value, name)
		}
	}
	return result
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
