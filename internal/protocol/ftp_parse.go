package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"strconv"
	"strings"
	"time"
)

// certFingerprintOf returns the SHA256 pin fingerprint of a DER certificate.
func certFingerprintOf(der []byte) string {
	sum := sha256.Sum256(der)
	return "SHA256:" + hex.EncodeToString(sum[:])
}

// parseLISTOutput parses traditional UNIX-style and DOS-style LIST output.
// It is best-effort: filenames are preserved even when metadata is absent.
func parseLISTOutput(data, dir string) []Entry {
	var out []Entry
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// Skip "total N" lines.
		if fields[0] == "total" {
			continue
		}

		// DOS-style: MM-DD-YY  HH:MM(AM|PM) <DIR|size> name
		if len(fields) >= 4 && isDOSDate(fields[0]) {
			e := parseDOSEntry(fields, dir)
			if e != nil {
				out = append(out, *e)
			}
			continue
		}

		// UNIX-style: perm links owner group size date time name
		if len(fields) < 9 || !strings.ContainsAny(fields[0], "dlsrwx-") {
			continue
		}
		perm := fields[0]
		e := Entry{
			Owner: fields[2],
			Group: fields[3],
			Permissions: strings.Map(func(r rune) rune {
				if r == 'r' || r == 'w' || r == 'x' || r == '-' {
					return r
				}
				return -1
			}, perm),
		}
		if len(e.Permissions) > 9 {
			e.Permissions = e.Permissions[1:10]
		}
		size, _ := strconv.ParseInt(fields[4], 10, 64)
		e.Size = size
		e.ModTime = parseLISTDate(fields[5], fields[6], fields[7])
		name := strings.Join(fields[8:], " ")
		if name == "." || name == ".." {
			continue
		}
		e.Name = name
		e.Path = path.Join(dir, name)
		e.MIMEType = GuessMIME(name)
		switch perm[0] {
		case 'd':
			e.Type = EntryDir
			e.Size = 0
		case 'l':
			e.Type = EntrySymlink
			e.IsSymlink = true
			if i := strings.Index(name, " -> "); i >= 0 {
				e.Name = name[:i]
				e.LinkTarget = name[i+4:]
				e.Path = path.Join(dir, e.Name)
				e.MIMEType = GuessMIME(e.Name)
			}
		default:
			e.Type = EntryFile
		}
		out = append(out, e)
	}
	return out
}

func parseDOSEntry(fields []string, dir string) *Entry {
	// fields: date time <DIR>|size name...
	idx := 2
	isDir := false
	if strings.EqualFold(fields[idx], "<DIR>") {
		isDir = true
		idx++
	} else {
		// size
		idx++
	}
	name := strings.Join(fields[idx:], " ")
	if name == "." || name == ".." || name == "" {
		return nil
	}
	e := &Entry{Name: name, Path: path.Join(dir, name), MIMEType: GuessMIME(name)}
	if isDir {
		e.Type = EntryDir
	} else {
		e.Type = EntryFile
		if n, err := strconv.ParseInt(fields[2], 10, 64); err == nil {
			e.Size = n
		}
	}
	return e
}

func isDOSDate(s string) bool {
	if len(s) != 8 || s[2] != '-' || s[5] != '-' {
		return false
	}
	for i, r := range s {
		if i == 2 || i == 5 {
			continue
		}
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// parseLISTDate handles both "Mon DD YYYY"/"Mon DD HH:MM" layouts.
func parseLISTDate(a, b, c string) time.Time {
	// Layout: <month> <day> <year|time>
	layouts := []string{"Jan _2 15:04", "Jan _2 2006"}
	joined := a + " " + b + " " + c
	for _, l := range layouts {
		if t, err := time.ParseInLocation(l, joined, time.Local); err == nil {
			if year := t.Year(); year == 0 {
				// "Jan 2 15:04" defaults to year 0; assume within a year.
				now := time.Now()
				t = t.AddDate(now.Year(), 0, 0)
				if t.After(now.Add(24 * time.Hour)) {
					t = t.AddDate(-1, 0, 0)
				}
			}
			return t.UTC()
		}
	}
	return time.Time{}
}
