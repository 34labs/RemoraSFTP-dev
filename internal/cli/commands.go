package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"remorasftp/internal/app"
	"remorasftp/internal/config"
	"remorasftp/internal/server"
	"remorasftp/internal/transfers"
	"remorasftp/internal/version"
)

func runVersion() error {
	info := version.Get()
	fmt.Printf("%s %s\n", info.Name, info.Version)
	fmt.Printf("  commit:    %s\n", info.Commit)
	fmt.Printf("  built:     %s\n", info.BuildTime)
	fmt.Printf("  go:        %s\n", info.GoVersion)
	fmt.Printf("  platform:  %s/%s\n", info.OS, info.Arch)
	return nil
}

func runStatus() error {
	inst, err := server.ReadInstanceFile()
	if err != nil {
		fmt.Println("RemoraSFTP engine: not running")
		fmt.Println("  Start it with: remorasftp start")
		return nil
	}
	fmt.Printf("RemoraSFTP engine: running\n  URL: %s\n  PID: %d\n", inst.URL, inst.PID)
	return nil
}

func runProfiles() error {
	eng, err := app.New()
	if err != nil {
		return err
	}
	defer eng.Shutdown()
	profiles := eng.Manager.Profiles()
	if len(profiles) == 0 {
		fmt.Println("No connection profiles yet. Create one with the browser UI (`remorasftp start`).")
		return nil
	}
	fmt.Printf("%-20s %-7s %-26s %s\n", "NAME", "PROTO", "HOST", "USER")
	for _, p := range profiles {
		fmt.Printf("%-20s %-7s %-26s %s\n",
			p.Name, p.Protocol, fmt.Sprintf("%s:%d", p.Host, p.Port), p.Username)
	}
	return nil
}

func runConnect(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: remorasftp connect <profile>")
	}
	eng, err := app.New()
	if err != nil {
		return err
	}
	defer eng.Shutdown()
	p, err := findProfile(eng, args[0])
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sess, err := eng.Manager.Connect(ctx, p.ID)
	if err != nil {
		return describeConnectErr(err)
	}
	defer eng.Manager.Disconnect(sess.ID)
	fmt.Printf("Connected to %s (%s) as %s\n", p.Name, sess.Server.Host, sess.Server.Username)
	fmt.Printf("  protocol:  %s (encrypted=%v)\n", sess.Protocol, sess.Caps.Encrypted)
	fmt.Printf("  start dir: %s\n", sess.StartDir)
	return nil
}

func runLs(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: remorasftp ls <profile> [path]")
	}
	dir := "/"
	if len(args) > 1 {
		dir = args[1]
	}
	return withSession(args[0], func(eng *app.Engine, sessID string) error {
		entries, err := eng.Manager.List(context.Background(), sessID, dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			suffix := ""
			if e.Type == "dir" {
				suffix = "/"
			}
			perm := e.Permissions
			if perm == "" {
				perm = "---------"
			}
			fmt.Printf("%s %10d  %s  %s%s\n", perm, e.Size,
				e.ModTime.Format("2006-01-02 15:04"), e.Name, suffix)
		}
		return nil
	})
}

func runPut(args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("usage: remorasftp put <profile> <local-path> <remote-path>")
	}
	return withSession(args[0], func(eng *app.Engine, sessID string) error {
		return runUpload(eng, sessID, args[1], args[2])
	})
}

func runGet(args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("usage: remorasftp get <profile> <remote-path> <local-path>")
	}
	return withSession(args[0], func(eng *app.Engine, sessID string) error {
		return runDownload(eng, sessID, args[1], args[2])
	})
}

func runMkdir(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: remorasftp mkdir <profile> <remote-path>")
	}
	return withSession(args[0], func(eng *app.Engine, sessID string) error {
		if err := eng.Manager.Mkdir(context.Background(), sessID, args[1]); err != nil {
			return err
		}
		fmt.Printf("created %s\n", args[1])
		return nil
	})
}

func runRm(args []string, fl *Flags) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: sftp rm <profile> <remote-path> [--recursive]")
	}
	recursive := fl.Bool("recursive")
	return withSession(args[0], func(eng *app.Engine, sessID string) error {
		if err := eng.Manager.Remove(context.Background(), sessID, args[1], recursive); err != nil {
			return err
		}
		fmt.Printf("deleted %s\n", args[1])
		return nil
	})
}

func runMv(args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("usage: remorasftp mv <profile> <from> <to>")
	}
	return withSession(args[0], func(eng *app.Engine, sessID string) error {
		if err := eng.Manager.Rename(context.Background(), sessID, args[1], args[2]); err != nil {
			return err
		}
		fmt.Printf("renamed %s -> %s\n", args[1], args[2])
		return nil
	})
}

// withSession builds the engine, resolves a profile, connects, runs fn, and
// disconnects.
func withSession(ref string, fn func(eng *app.Engine, sessionID string) error) error {
	eng, err := app.New()
	if err != nil {
		return err
	}
	defer eng.Shutdown()
	p, err := findProfile(eng, ref)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	sess, err := eng.Manager.Connect(ctx, p.ID)
	if err != nil {
		return describeConnectErr(err)
	}
	defer eng.Manager.Disconnect(sess.ID)
	return fn(eng, sess.ID)
}

func findProfile(eng *app.Engine, ref string) (*config.Connection, error) {
	for _, p := range eng.Manager.Profiles() {
		if p.ID == ref || p.Name == ref {
			return p, nil
		}
	}
	return nil, fmt.Errorf("no connection profile named or identified by %q", ref)
}

func describeConnectErr(err error) error {
	if msg := trustPromptMessage(err); msg != "" {
		return fmt.Errorf("%s\n\nThis server's identity is not yet trusted. Connect once via the browser\nUI (`remorasftp start`) to review and accept the fingerprint, then retry.", msg)
	}
	return err
}

func runUpload(eng *app.Engine, sessionID, local, remote string) error {
	f, err := os.Open(local)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	cl, err := eng.Manager.Client(sessionID)
	if err != nil {
		return err
	}
	j := eng.Transfer.EnqueueStreamUpload(sessionID, remote, filepath.Base(local), st.Size(), f, cl.Capabilities())
	waitFor(eng, j.ID)
	fmt.Printf("uploaded %s -> %s\n", local, remote)
	return nil
}

func runDownload(eng *app.Engine, sessionID, remote, local string) error {
	cl, err := eng.Manager.Client(sessionID)
	if err != nil {
		return err
	}
	open, _, err := transfers.LocalDownloadWriter(filepath.Dir(local), filepath.Base(local))
	if err != nil {
		return err
	}
	j := eng.Transfer.EnqueueDownload(&transfers.Job{
		SessionID:  sessionID,
		RemotePath: remote,
		Name:       filepath.Base(local),
		LocalPath:  local,
		Caps:       cl.Capabilities(),
	}, func(ctx context.Context, offset int64) (io.WriteCloser, error) {
		return open(ctx, offset)
	})
	waitFor(eng, j.ID)
	fmt.Printf("downloaded %s -> %s\n", remote, local)
	return nil
}

func waitFor(eng *app.Engine, id string) {
	for {
		time.Sleep(200 * time.Millisecond)
		for _, j := range eng.Transfer.List() {
			if j.ID != id {
				continue
			}
			switch j.Status {
			case transfers.StatusCompleted:
				fmt.Fprintln(os.Stderr)
				return
			case transfers.StatusFailed:
				fmt.Fprintf(os.Stderr, "\ntransfer failed: %s\n", j.Error)
				os.Exit(1)
			case transfers.StatusCanceled:
				fmt.Fprintln(os.Stderr, "\ntransfer canceled")
				os.Exit(1)
			}
			if j.Total > 0 {
				fmt.Fprintf(os.Stderr, "\r  %d%%  %s   ", j.Done*100/j.Total, humanSpeed(j.Speed))
			}
		}
	}
}

func humanSpeed(bps int64) string {
	const unit = 1024
	if bps < unit {
		return fmt.Sprintf("%d B/s", bps)
	}
	div, exp := int64(unit), 0
	for n := bps / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB/s", float64(bps)/float64(div), "KMGTPE"[exp])
}
