// Package timeline reads every macOS WeChat 3.x message table, including tables
// whose chat identity is absent from the session list.
package timeline

import (
	"container/heap"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/sjzar/chatlog/internal/model"
)

var Beijing = time.FixedZone("Asia/Shanghai", 8*60*60)
var ErrUnsupported = errors.New("timeline supports macOS WeChat 3.x only")
var messageDB = regexp.MustCompile(`^msg_([0-9]{1,2})?\.db$`)
var messageTable = regexp.MustCompile(`^Chat_([0-9a-fA-F]{32})$`)

const DefaultLimit = 500
const MaxLimit = 5000

type Options struct {
	Start          time.Time
	End            time.Time
	Limit          int
	Cursor         string
	ExcludeTalkers []string
	ExcludeSenders []string
}

type Item struct {
	*model.Message
	Source           string `json:"source"`
	TableHash        string `json:"tableHash"`
	LocalID          int64  `json:"localId"`
	ServerID         string `json:"serverId"`
	IdentityResolved bool   `json:"identityResolved"`
	RawContent       string `json:"rawContent"`
}

type Page struct {
	Items            []*Item   `json:"items"`
	Start            time.Time `json:"start"`
	End              time.Time `json:"end"`
	Timezone         string    `json:"timezone"`
	HasMore          bool      `json:"has_more"`
	NextCursor       string    `json:"next_cursor"`
	TablesScanned    int       `json:"tables_scanned"`
	TablesExcluded   int       `json:"tables_excluded"`
	UnresolvedItems  int       `json:"unresolved_items"`
	IdentityWarnings int       `json:"identity_warnings"`
}

type cursor struct {
	Version int    `json:"v"`
	Start   int64  `json:"start"`
	End     int64  `json:"end"`
	Time    int64  `json:"time"`
	Source  string `json:"source"`
	ID      int64  `json:"id"`
	Filters string `json:"filters,omitempty"`
}

func normalizeOptions(opt Options) (Options, error) {
	normalize := func(ids []string) ([]string, error) {
		seen := make(map[string]bool)
		for _, id := range ids {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if len(id) > 256 || strings.ContainsAny(id, ",\x00\r\n") {
				return nil, errors.New("invalid excluded ID")
			}
			seen[id] = true
		}
		if len(seen) > 100 {
			return nil, errors.New("at most 100 excluded IDs per filter")
		}
		var result []string
		for id := range seen {
			result = append(result, id)
		}
		sort.Strings(result)
		return result, nil
	}
	var err error
	opt.ExcludeTalkers, err = normalize(opt.ExcludeTalkers)
	if err != nil {
		return opt, err
	}
	opt.ExcludeSenders, err = normalize(opt.ExcludeSenders)
	return opt, err
}

func filterDigest(opt Options) string {
	if len(opt.ExcludeTalkers) == 0 && len(opt.ExcludeSenders) == 0 {
		return ""
	}
	data, _ := json.Marshal([][]string{opt.ExcludeTalkers, opt.ExcludeSenders})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// ParseRange treats unzoned dates and timestamps as Beijing time. End is exclusive.
func ParseRange(start, end string) (time.Time, time.Time, error) {
	parse := func(value string) (time.Time, error) {
		for _, layout := range []string{"2006-01-02", "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
			if t, err := time.ParseInLocation(layout, value, Beijing); err == nil && t.Nanosecond() == 0 {
				return t, nil
			}
		}
		t, err := time.Parse(time.RFC3339, value)
		if err != nil || t.Nanosecond() != 0 {
			return time.Time{}, errors.New("expected a date or a timestamp with whole seconds")
		}
		return t.In(Beijing), nil
	}
	a, err := parse(start)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	b, err := parse(end)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if !a.Before(b) {
		return time.Time{}, time.Time{}, errors.New("start must be before end")
	}
	return a, b, nil
}

func decodeCursor(opt Options) (*cursor, error) {
	if opt.Cursor == "" {
		return nil, nil
	}
	if len(opt.Cursor) > 4096 {
		return nil, errors.New("cursor is too long")
	}
	data, err := base64.RawURLEncoding.DecodeString(opt.Cursor)
	if err != nil {
		return nil, errors.New("invalid cursor")
	}
	var c cursor
	if err := json.Unmarshal(data, &c); err != nil || c.Version != 1 || c.Start != opt.Start.Unix() || c.End != opt.End.Unix() || c.Time < c.Start || c.Time >= c.End || c.Source == "" || c.ID < 0 || c.Filters != filterDigest(opt) {
		return nil, errors.New("invalid cursor or cursor time range mismatch")
	}
	return &c, nil
}

func Validate(opt Options) error {
	var err error
	opt, err = normalizeOptions(opt)
	if err != nil {
		return err
	}
	if !opt.Start.Before(opt.End) || opt.Start.Nanosecond() != 0 || opt.End.Nanosecond() != 0 {
		return errors.New("invalid time interval")
	}
	if opt.Limit < 1 || opt.Limit > MaxLimit {
		return errors.New("limit must be between 1 and 5000")
	}
	_, err = decodeCursor(opt)
	return err
}

func openReadOnly(ctx context.Context, path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := &url.URL{Scheme: "file", Path: abs, RawQuery: "mode=ro&_query_only=1&_busy_timeout=5000"}
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func quote(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

func less(a, b *Item) bool {
	if a.Time.Unix() != b.Time.Unix() {
		return a.Time.Unix() < b.Time.Unix()
	}
	if a.Source != b.Source {
		return a.Source < b.Source
	}
	return a.LocalID < b.LocalID
}

type newestFirst []*Item

func (h newestFirst) Len() int           { return len(h) }
func (h newestFirst) Less(i, j int) bool { return less(h[j], h[i]) }
func (h newestFirst) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *newestFirst) Push(x any)        { *h = append(*h, x.(*Item)) }
func (h *newestFirst) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

type identity struct{ user, name string }

type schema struct{ databases, identities []string }

func discover(root string) (schema, error) {
	var found schema
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if messageDB.MatchString(d.Name()) {
			found.databases = append(found.databases, path)
		}
		switch d.Name() {
		case "wccontact_new2.db", "group_new.db", "session_new.db":
			found.identities = append(found.identities, path)
		}
		return nil
	})
	if err != nil {
		return found, err
	}
	if len(found.databases) == 0 {
		return found, errors.New("no message databases found")
	}
	sort.Strings(found.databases)
	sort.Strings(found.identities)
	return found, nil
}

func loadIdentities(ctx context.Context, paths []string) (map[string]identity, int) {
	users := make(map[string]identity)
	warnings := 0
	for _, path := range paths {
		db, err := openReadOnly(ctx, path)
		if err != nil {
			warnings++
			continue
		}
		for _, table := range []string{"WCContact", "GroupMember", "GroupContact", "SessionAbstract"} {
			rows, err := db.QueryContext(ctx, "PRAGMA table_info("+quote(table)+")")
			if err != nil {
				warnings++
				continue
			}
			columns := make(map[string]bool)
			for rows.Next() {
				var cid, notNull, pk int
				var name, typ string
				var defaultValue any
				if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
					warnings++
					continue
				}
				columns[name] = true
			}
			if rows.Err() != nil {
				warnings++
			}
			rows.Close()
			if !columns["m_nsUsrName"] {
				continue
			}
			name := `''`
			for _, column := range []string{"nickname", "m_nsNickName", "m_nsRemark"} {
				if columns[column] {
					name = "COALESCE(NULLIF(" + quote(column) + ",'')," + name + ")"
				}
			}
			rows, err = db.QueryContext(ctx, "SELECT m_nsUsrName,"+name+" FROM "+quote(table))
			if err != nil {
				warnings++
				continue
			}
			for rows.Next() {
				var user, display sql.NullString
				if rows.Scan(&user, &display) != nil {
					warnings++
					continue
				}
				if !user.Valid || user.String == "" {
					continue
				}
				hash := fmt.Sprintf("%x", md5.Sum([]byte(user.String)))
				old := users[hash]
				if old.name == "" || display.String != "" {
					users[hash] = identity{user.String, display.String}
				}
			}
			if rows.Err() != nil {
				warnings++
			}
			rows.Close()
		}
		db.Close()
	}
	return users, warnings
}

// Query returns a bounded global page. Every source failure fails the request;
// unavailable tables are never silently mistaken for an empty conversation.
func Query(ctx context.Context, root string, opt Options) (*Page, error) {
	var err error
	opt, err = normalizeOptions(opt)
	if err != nil {
		return nil, err
	}
	if err := Validate(opt); err != nil {
		return nil, err
	}
	cur, _ := decodeCursor(opt)
	found, err := discover(root)
	if err != nil {
		return nil, err
	}
	users, warnings := loadIdentities(ctx, found.identities)
	page := &Page{Items: []*Item{}, Start: opt.Start.In(Beijing), End: opt.End.In(Beijing), Timezone: "Asia/Shanghai", IdentityWarnings: warnings}
	best := &newestFirst{}
	capacity := opt.Limit + 1
	for _, path := range found.databases {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		db, err := openReadOnly(ctx, path)
		if err != nil {
			return nil, err
		}
		err = scanDatabase(ctx, db, root, path, opt, cur, users, best, capacity, page)
		db.Close()
		if err != nil {
			return nil, err
		}
	}
	page.Items = append(page.Items, (*best)...)
	sort.Slice(page.Items, func(i, j int) bool { return less(page.Items[i], page.Items[j]) })
	if len(page.Items) > opt.Limit {
		page.HasMore = true
		page.Items = page.Items[:opt.Limit]
	}
	for _, item := range page.Items {
		if !item.IdentityResolved {
			page.UnresolvedItems++
		}
	}
	if page.HasMore {
		last := page.Items[len(page.Items)-1]
		data, _ := json.Marshal(cursor{Version: 1, Start: opt.Start.Unix(), End: opt.End.Unix(), Time: last.Time.Unix(), Source: last.Source, ID: last.LocalID, Filters: filterDigest(opt)})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	return page, nil
}

func scanDatabase(ctx context.Context, db *sql.DB, root, path string, opt Options, cur *cursor, users map[string]identity, best *newestFirst, capacity int, page *Page) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		if messageTable.MatchString(name) {
			tables = append(tables, name)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	excludedTables := make(map[string]bool)
	for _, user := range opt.ExcludeTalkers {
		excludedTables[fmt.Sprintf("%x", md5.Sum([]byte(user)))] = true
	}
	excludedSenders := make(map[string]bool)
	for _, user := range opt.ExcludeSenders {
		excludedSenders[user] = true
	}
	for _, table := range tables {
		source := filepath.ToSlash(relative) + ":" + table
		tableHash := strings.ToLower(messageTable.FindStringSubmatch(table)[1])
		if excludedTables[tableHash] {
			page.TablesExcluded++
			continue
		}
		page.TablesScanned++
		query := "SELECT mesLocalID,COALESCE(mesSvrID,0),msgCreateTime,COALESCE(msgContent,''),COALESCE(messageType,0),COALESCE(mesDes,0) FROM " + quote(table) + " WHERE msgCreateTime>=? AND msgCreateTime<?"
		args := []any{opt.Start.Unix(), opt.End.Unix()}
		if cur != nil {
			switch strings.Compare(source, cur.Source) {
			case -1:
				query += " AND msgCreateTime>?"
				args = append(args, cur.Time)
			case 0:
				query += " AND (msgCreateTime>? OR (msgCreateTime=? AND mesLocalID>?))"
				args = append(args, cur.Time, cur.Time, cur.ID)
			case 1:
				query += " AND msgCreateTime>=?"
				args = append(args, cur.Time)
			}
		}
		if best.Len() == capacity {
			query += " AND msgCreateTime<=?"
			args = append(args, (*best)[0].Time.Unix())
		}
		query += " ORDER BY msgCreateTime ASC,mesLocalID ASC"
		// Sender filtering needs to examine past excluded rows before filling a page.
		if len(excludedSenders) == 0 {
			query += " LIMIT ?"
			args = append(args, capacity)
		}
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var raw model.MessageDarwinV3
			var localID, serverID int64
			if err := rows.Scan(&localID, &serverID, &raw.MsgCreateTime, &raw.MsgContent, &raw.MessageType, &raw.MesDes); err != nil {
				rows.Close()
				return err
			}
			person, resolved := users[tableHash]
			message := raw.Wrap(person.user)
			if excludedSenders[message.Sender] {
				continue
			}
			message.Time = message.Time.In(Beijing)
			message.TalkerName = person.name
			if person.user != "" && message.Sender == person.user {
				message.SenderName = person.name
			}
			if message.Sender != "" {
				senderHash := fmt.Sprintf("%x", md5.Sum([]byte(message.Sender)))
				if sender, ok := users[senderHash]; ok {
					message.SenderName = sender.name
				}
			}
			item := &Item{Message: message, Source: source, TableHash: tableHash, LocalID: localID, ServerID: strconv.FormatInt(serverID, 10), IdentityResolved: resolved, RawContent: raw.MsgContent}
			if best.Len() < capacity {
				heap.Push(best, item)
			} else if less(item, (*best)[0]) {
				heap.Pop(best)
				heap.Push(best, item)
			} else {
				break
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
