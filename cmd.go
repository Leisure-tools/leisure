package main

import (
	"os"

	"github.com/alecthomas/kong"
)

type GlobalOpts struct {
	UnixSocket string `short:u help:"Path to UNIX socket -- will be created and must not exist beforehand" type:path`
	Verbose    int    `short:v help:Verbose type:counter`
	Cookies    string `help:"Path to cookies file" type:path`
	Lock       bool   `help:"Lock the cookies file"`
	ForceLock  bool   `help:"Lock the cookies file and remove other locks"`
	Parent     int    `help:"Parent process using leisure, in case the parent is not the real owner"`
	Host       string `help:"Host of Leisure peer"`
	Port       int    `help:"Port of Leisure peer"`
	ctx        *kong.Context
}

func initGlobalOpts(cli *CLI) {
	opts := &cli.globals
	cli.Parse.GlobalOpts = opts
	cli.Get.GlobalOpts = opts
	cli.Doc.GlobalOpts = opts
	cli.Session.GlobalOpts = opts
}

func (cli *CLI) defaults() {
	opts := &cli.globals
	if opts.Port == 0 {
		opts.Port = DEFAULT_PORT
	}
	if opts.UnixSocket == "" {
		if dir, err := os.UserHomeDir(); err == nil {
			opts.UnixSocket = dir + "/" + DEFAULT_UNIX_SOCKET
		} else {
			panic("Could not obtain home directory")
		}
	}
	if cli.Peer.Port == 0 {
		cli.Peer.Port = DEFAULT_PORT
	}
	if cli.Peer.UnixSocket == "" {
		if dir, err := os.UserHomeDir(); err == nil {
			cli.Peer.UnixSocket = dir + "/" + DEFAULT_UNIX_SOCKET
		} else {
			panic("Could not obtain home directory")
		}
	}
	if cli.Session.Update.Timeout == 0 {
		cli.Session.Update.Timeout = 1000 * 60 * 2
	}
}

type CLI struct {
	globals GlobalOpts
	Stop    StopCmd  `cmd help:"Stop the peer"`
	Peer    PeerCmd  `cmd help:"Run a leisure peer on unix domain socket PATH and, optionally, on a TCP port."`
	Parse   ParseCmd `cmd help:"Parse an org document. Example: leisure get /default.org | leisure parse"`
	Get     GetCmd   `cmd help:"HTTP get request to leisure server"`
	Doc     struct {
		*GlobalOpts
		List   DocListCmd   `cmd help:"List all documents"`
		Create DocCreateCmd `cmd help:"Share a document from stdin"`
		Get    DocGetCmd    `cmd help:"Get a document"`
	} `cmd help:"Document commands"`
	Session struct {
		*GlobalOpts
		List    SessionListCmd    `cmd help:"List all sessions"`
		Create  SessionCreateCmd  `cmd help:"Create a session"`
		Doc     SessionDocCmd     `cmd help:"Get a session's document"`
		Get     SessionGetCmd     `cmd help:"Get data from a session's document"`
		Set     SessionSetCmd     `cmd help:"Set data in a session's document using a JSON value from stdin"`
		Connect SessionConnectCmd `cmd help:"Connect to a session"`
		Edit    SessionEditCmd    `cmd help:"Add edits to a session"`
		Refresh SessionRefreshCmd `cmd help:"Send a null edit to a sesison"`
		Update  SessionUpdateCmd  `cmd help:"Check if a session has updates"`
		Unlock  SessionUnlockCmd  `cmd help:"Unlock a session"`
		Tag     SessionTagCmd     `cmd help:"Get tagged data from a session's document"`
	} `cmd help:"Session commands"`
}

type PeerCmd struct {
	UnixSocket string `short:u help:"Path to UNIX socket -- will be created and must not exist beforehand" type:path`
	Verbose    int    `short:v help:Verbose type:counter`
	Port       int    `short:l name:listen help:"TCP Port to listen on"`
	ofs        *Overlay
	Html       string `help:"DIRECTORY to serve files from" type:path`
}

type StopCmd struct {
	UnixSocket string `short:u help:"Path to UNIX socket -- will be created and must not exist beforehand" type:path`
	Port       int    `short:l name:listen help:"TCP Port to listen on"`
	Host       string `help:"Host of Leisure peer"`
	Verbose    int    `short:v help:Verbose type:counter`
}

type DocListCmd struct{}

type DocCreateCmd struct {
	DocId string `arg optional name:id help:"ID of document"`
	Alias string `help:"Alias for document"`
}

type DocGetCmd struct {
	DocId string `arg name:id help:"ID, alias, or hash of document"`
	Hash  string `help:"ID is a document hash"`
	Dump  bool   `help:"Request a dump of an org document instead of the document itself"`
	Org   bool   `help:"Request document in org format"`
	Data  bool   `help:"Request document data"`
}

type SessionListCmd struct{}

type DocConnectionArgs struct {
	Session   string `arg name:session help:"Session ID"`
	DocId     string `arg name:docid help:"Document ID or alais"`
	Org       bool   `help:"Receive org changes"`
	NoStrings bool   `help:"Do not receive string changes"`
	Data      bool   `help:"Receive only data changes"`
}

type SessionCreateCmd struct {
	DocConnectionArgs
}

type SessionConnectCmd struct {
	DocConnectionArgs
	Force bool `short:f help:"Force connection, even if session is in use"`
}

type SessionDocCmd struct{}

type SessionGetCmd struct {
	Name string `arg help:"Data block name"`
}

type SessionSetCmd struct {
	Name     string `arg optional help:"Data block name"`
	Multiple bool   `short:m help:"Set multiple data, input is an object with keys for data names"`
}

type SessionEditCmd struct{}

type SessionRefreshCmd struct{}

type SessionUpdateCmd struct {
	Timeout int `help:"Timeout in milliseconds, defaults to 2 minutes"`
}

type SessionUnlockCmd struct{}

type SessionTagCmd struct {
	Name string `arg help:"Tag for data blocks"`
}

type ParseCmd struct {
	*GlobalOpts
}

type GetCmd struct {
	*GlobalOpts
	URL string `arg help:"URL to get from leisure server"`
}
