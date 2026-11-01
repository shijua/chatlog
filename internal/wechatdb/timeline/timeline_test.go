package timeline

import (
	"context"
	"crypto/md5"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func makeDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func exec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func table(user string) string { return "Chat_" + fmt.Sprintf("%x", md5.Sum([]byte(user))) }

func fixture(t *testing.T) (string, Options) {
	t.Helper()
	root := t.TempDir()
	start, end, err := ParseRange("2026-11-01", "2026-11-02")
	if err != nil {
		t.Fatal(err)
	}
	opt := Options{Start: start, End: end, Limit: 500}
	private := table("wxid_private")
	unknown := table("unknown-person")
	group := table("group@chatroom")
	for i, names := range [][]string{{private}, {private, unknown, group}} {
		db := makeDB(t, filepath.Join(root, "Message", "msg_"+strconv.Itoa(i)+".db"))
		for _, name := range names {
			exec(t, db, "CREATE TABLE "+quote(name)+" (mesLocalID INTEGER PRIMARY KEY,mesSvrID INTEGER,msgCreateTime INTEGER,msgContent TEXT,messageType INTEGER,mesDes INTEGER)")
			exec(t, db, "CREATE INDEX "+quote(name+"_time")+" ON "+quote(name)+" (msgCreateTime)")
		}
		if i == 0 {
			for _, stamp := range []int64{start.Unix() - 1, start.Unix(), start.Unix() + 1, start.Unix() + 1, end.Unix()} {
				exec(t, db, "INSERT INTO "+quote(private)+" (mesSvrID,msgCreateTime,msgContent,messageType,mesDes) VALUES (?,?,?,?,?)", int64(9007199254740993), stamp, "same content", 1, 1)
			}
		} else {
			for _, name := range names {
				body := "hidden content"
				if name == group {
					body = "wxid_sender:\nhello group"
				}
				exec(t, db, "INSERT INTO "+quote(name)+" VALUES (?,?,?,?,?,?)", 1, 99, start.Unix()+1, body, 1, 1)
			}
		}
	}
	db := makeDB(t, filepath.Join(root, "Contact", "wccontact_new2.db"))
	exec(t, db, "CREATE TABLE WCContact (m_nsUsrName TEXT,nickname TEXT,m_nsRemark TEXT)")
	exec(t, db, "INSERT INTO WCContact VALUES ('wxid_private','Private name','Remark')")
	db = makeDB(t, filepath.Join(root, "Contact", "group_new.db"))
	exec(t, db, "CREATE TABLE GroupContact (m_nsUsrName TEXT,nickname TEXT,m_nsRemark TEXT)")
	exec(t, db, "INSERT INTO GroupContact VALUES ('group@chatroom','Hidden group','')")
	exec(t, db, "CREATE TABLE GroupMember (m_nsUsrName TEXT,nickname TEXT)")
	exec(t, db, "INSERT INTO GroupMember VALUES ('wxid_sender','Sender')")
	return root, opt
}

func TestBeijingRange(t *testing.T) {
	start, end, err := ParseRange("2026-11-01", "2026-11-02")
	if err != nil {
		t.Fatal(err)
	}
	if start.UTC().Format(time.RFC3339) != "2026-10-31T16:00:00Z" || end.Sub(start) != 24*time.Hour {
		t.Fatal("incorrect Beijing boundaries")
	}
	for _, pair := range [][2]string{{"", "2026-11-02"}, {"2026-11-02", "2026-11-01"}, {"2026-11-01T00:00:00.1", "2026-11-02"}, {"2026-02-30", "2026-11-02"}} {
		if _, _, err := ParseRange(pair[0], pair[1]); err == nil {
			t.Fatal("invalid interval accepted")
		}
	}
	a, b, err := ParseRange("2026-11-01T10:20:30", "2026-11-01 10:21:30")
	if err != nil || b.Sub(a) != time.Minute {
		t.Fatal("second-granularity interval failed")
	}
}

func TestAllTablesIncludesHiddenUnknownAndBothShards(t *testing.T) {
	root, opt := fixture(t)
	page, err := Query(context.Background(), root, opt)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 6 || page.TablesScanned != 4 || page.HasMore {
		t.Fatalf("unexpected coverage: %d items, %d tables", len(page.Items), page.TablesScanned)
	}
	if page.UnresolvedItems != 1 || page.IdentityWarnings != 0 {
		t.Fatal("unknown identities not explicit")
	}
	groupFound := false
	largeIDFound := false
	for _, item := range page.Items {
		if item.Time.Before(opt.Start) || !item.Time.Before(opt.End) {
			t.Fatal("boundary leaked")
		}
		if !item.IdentityResolved && (item.TableHash == "" || item.RawContent == "" || item.Talker != "") {
			t.Fatal("unknown identity lost data")
		}
		if item.ServerID == "9007199254740993" {
			largeIDFound = true
		}
		if item.IsChatRoom {
			groupFound = true
			if item.Sender != "wxid_sender" || item.Content != "hello group" {
				t.Fatal("group message was not parsed")
			}
		}
	}
	if !groupFound || !largeIDFound {
		t.Fatal("missing group or exact server ID")
	}
}

func TestCursorPaginatesSameSecondWithoutOmissionOrDeduplication(t *testing.T) {
	root, opt := fixture(t)
	all, err := Query(context.Background(), root, opt)
	if err != nil {
		t.Fatal(err)
	}
	opt.Limit = 2
	var got []*Item
	for n := 0; n < 10; n++ {
		page, err := Query(context.Background(), root, opt)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, page.Items...)
		if !page.HasMore {
			break
		}
		if page.NextCursor == "" {
			t.Fatal("missing cursor")
		}
		opt.Cursor = page.NextCursor
	}
	if len(got) != len(all.Items) {
		t.Fatalf("got %d, want %d", len(got), len(all.Items))
	}
	for i, item := range got {
		expected := all.Items[i]
		if item.Source != expected.Source || item.LocalID != expected.LocalID {
			t.Fatal("pagination changed order or lost a record")
		}
	}
	opt.Limit = 1
	opt.Cursor = ""
	page, err := Query(context.Background(), root, opt)
	if err != nil {
		t.Fatal(err)
	}
	opt.Cursor = page.NextCursor
	opt.End = opt.End.Add(time.Hour)
	if _, err := Query(context.Background(), root, opt); err == nil {
		t.Fatal("cursor reused for a different interval")
	}
}

func TestReadFailureNeverReturnsPartialSuccess(t *testing.T) {
	root, opt := fixture(t)
	if err := os.WriteFile(filepath.Join(root, "msg_2.db"), []byte("not SQLite"), 0600); err != nil {
		t.Fatal(err)
	}
	if page, err := Query(context.Background(), root, opt); err == nil || page != nil {
		t.Fatal("corrupt source silently skipped")
	}
}

func TestCancellationAndReadOnly(t *testing.T) {
	root, opt := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Query(ctx, root, opt); err == nil {
		t.Fatal("cancelled query succeeded")
	}
	db, err := openReadOnly(context.Background(), filepath.Join(root, "Message/msg_0.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE forbidden (id INTEGER)"); err == nil {
		t.Fatal("read-only connection allowed writes")
	}
}
