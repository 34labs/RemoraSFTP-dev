package protocol

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// startTestFTPServer launches a minimal but functional FTP server backed by a
// temp directory. It supports USER/PASS, PASV/EPSV, LIST, RETR, STOR, APPE,
// MKD, RMD, DELE, RNFR/RNTO, SIZE, MDTM, PWD, CWD, TYPE, REST, FEAT, QUIT.
func startTestFTPServer(t *testing.T) (hostport string, cleanup func()) {
	t.Helper()
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "seed.txt"), []byte("ftp seed"), 0o644)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hostport = ln.Addr().String()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveFTPConn(conn, root)
		}
	}()

	return hostport, func() { ln.Close() }
}

// passiveSession holds a waiting passive listener.
type passiveSession struct {
	listener net.Listener
}

var passiveMu sync.Mutex
var passiveWaiters []*passiveSession

func serveFTPConn(conn net.Conn, root string) {
	defer conn.Close()
	rd := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	send := func(code int, msg string) {
		fmt.Fprintf(w, "%d %s\r\n", code, msg)
		w.Flush()
	}
	send(220, "testftp ready")

	var renameFrom string
	cwd := "/"

	dataRoot := func(p string) string {
		if !strings.HasPrefix(p, "/") {
			p = cwd + "/" + p
		}
		clean := filepath.Clean("/" + strings.ReplaceAll(p, "\\", "/"))
		return filepath.Join(root, clean)
	}

	// pendingData is a channel delivering an accepted data connection once
	// the client connects.
	var pendingData chan net.Conn

	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}
		cmd := strings.ToUpper(line)
		arg := ""
		if i := strings.IndexByte(line, ' '); i >= 0 {
			cmd = strings.ToUpper(line[:i])
			arg = strings.TrimSpace(line[i+1:])
		}
		switch cmd {
		case "USER":
			send(331, "need password")
		case "PASS":
			send(230, "welcome")
		case "FEAT":
			fmt.Fprintf(w, "211-Features:\r\n SIZE\r\n MDTM\r\n REST STREAM\r\n211 End\r\n")
			w.Flush()
		case "SYST":
			send(215, "UNIX Type: L8")
		case "TYPE":
			send(200, "type set")
		case "PWD", "XPWD":
			fmt.Fprintf(w, "257 \"%s\" is current directory\r\n", cwd)
			w.Flush()
		case "CWD":
			if arg != "/" && arg != "" {
				cwd = "/" + strings.Trim(arg, "/")
			} else {
				cwd = "/"
			}
			send(250, "cwd set")
		case "EPSV", "PASV":
			pl, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				send(425, "no passive")
				continue
			}
			_, port, _ := net.SplitHostPort(pl.Addr().String())
			pn, _ := strconv.Atoi(port)
			dataCh := make(chan net.Conn, 1)
			pendingData = dataCh
			go func(pl net.Listener, ch chan net.Conn) {
				dc, err := pl.Accept()
				pl.Close()
				if err != nil {
					ch <- nil
					return
				}
				ch <- dc
			}(pl, dataCh)
			if cmd == "EPSV" {
				fmt.Fprintf(w, "229 Entering Extended Passive Mode (|||%d|)\r\n", pn)
			} else {
				fmt.Fprintf(w, "227 Entering Passive Mode (127,0,0,1,%d,%d)\r\n", pn/256, pn%256)
			}
			w.Flush()
		case "SIZE":
			info, err := os.Stat(dataRoot(arg))
			if err != nil {
				send(550, "not found")
			} else {
				fmt.Fprintf(w, "213 %d\r\n", info.Size())
				w.Flush()
			}
		case "MDTM":
			send(213, "20240102030405")
		case "MKD", "XMKD":
			if err := os.MkdirAll(dataRoot(arg), 0o755); err != nil {
				send(550, "mkdir failed")
			} else {
				send(257, "created")
			}
		case "RMD", "XRMD":
			os.RemoveAll(dataRoot(arg))
			send(250, "removed dir")
		case "DELE":
			os.Remove(dataRoot(arg))
			send(250, "deleted")
		case "RNFR":
			renameFrom = arg
			send(350, "ready")
		case "RNTO":
			_ = os.Rename(dataRoot(renameFrom), dataRoot(arg))
			send(250, "renamed")
		case "REST":
			send(350, "rest ok")
		case "SITE":
			if strings.HasPrefix(strings.ToUpper(arg), "CHMOD") {
				send(200, "chmod ok")
			} else {
				send(502, "unknown")
			}
		case "LIST", "MLSD", "RETR", "STOR", "APPE":
			if pendingData == nil {
				send(425, "no passive connection")
				continue
			}
			send(150, "opening data connection")
			dc := <-pendingData
			pendingData = nil
			if dc == nil {
				send(425, "data connection failed")
				continue
			}
			handleFTPData(dc, cmd, arg, root, cwd, send)
		case "ABOR":
			send(226, "aborted")
		case "QUIT":
			send(221, "bye")
			return
		default:
			send(502, "not implemented: "+cmd)
		}
	}
}

func handleFTPData(dc net.Conn, cmd, arg, root, cwd string, send func(int, string)) {
	defer dc.Close()
	real := func(p string) string {
		if !strings.HasPrefix(p, "/") {
			p = cwd + "/" + p
		}
		clean := filepath.Clean("/" + strings.ReplaceAll(p, "\\", "/"))
		return filepath.Join(root, clean)
	}
	switch cmd {
	case "LIST", "MLSD":
		dir := arg
		if dir == "" {
			dir = cwd
		}
		entries, _ := os.ReadDir(real(dir))
		for _, e := range entries {
			info, _ := e.Info()
			perm := "-rw-r--r--"
			if e.IsDir() {
				perm = "drwxr-xr-x"
			}
			fmt.Fprintf(dc, "%s 1 owner group %12d Jan  2  2024 %s\r\n", perm, info.Size(), e.Name())
		}
		send(226, "transfer done")
	case "RETR":
		f, err := os.Open(real(arg))
		if err != nil {
			send(550, "not found")
			return
		}
		_, _ = io.Copy(dc, f)
		f.Close()
		send(226, "transfer done")
	case "STOR", "APPE":
		_ = os.MkdirAll(filepath.Dir(real(arg)), 0o755)
		flag := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		if cmd == "APPE" {
			flag = os.O_WRONLY | os.O_CREATE | os.O_APPEND
		}
		f, err := os.OpenFile(real(arg), flag, 0o644)
		if err != nil {
			send(550, "cannot open")
			return
		}
		_, _ = io.Copy(f, dc)
		f.Close()
		send(226, "transfer done")
	}
}
