package http

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sjzar/chatlog/internal/wechatdb/timeline"
)

func TestTimelineHTTPValidatesBeforeQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, raw := range []string{"", "?start=bad&end=2026-11-02", "?start=2026-11-02&end=2026-11-01", "?start=2026-11-01&end=2026-11-02&limit=0", "?start=2026-11-01&end=2026-11-02&cursor=garbage", "?start=2026-11-01&end=2026-11-02&format=csv", "?start=2026-11-01&end=2026-11-02&exclude_talker=" + strings.Repeat("x", 257), "?start=2026-11-01&end=2026-11-02&exclude_sender=a&exclude_sender=b"} {
		t.Run(raw, func(t *testing.T) {
			r := gin.New()
			r.GET("/api/v1/timeline", timelineHandler(func(context.Context, timeline.Options) (*timeline.Page, error) {
				t.Fatal("invalid request reached database")
				return nil, nil
			}))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/timeline"+raw, nil))
			if w.Code != 400 {
				t.Fatalf("status %d", w.Code)
			}
		})
	}
}

func TestTimelineHTTPNoTalkerRequiredAndErrorsFailClosed(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{nil, 200}, {timeline.ErrUnsupported, 501}, {errors.New("private backend detail"), 500}} {
		r := gin.New()
		r.GET("/api/v1/timeline", timelineHandler(func(_ context.Context, opt timeline.Options) (*timeline.Page, error) {
			if opt.Start.Format("2006-01-02T15:04:05Z07:00") != "2026-11-01T00:00:00+08:00" || opt.Limit != 500 {
				t.Fatal("unexpected interval")
			}
			return &timeline.Page{Items: []*timeline.Item{}}, tc.err
		}))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/timeline?start=2026-11-01&end=2026-11-02", nil))
		if w.Code != tc.status {
			t.Fatalf("status %d, want %d", w.Code, tc.status)
		}
		if tc.status == 500 && strings.Contains(w.Body.String(), tc.err.Error()) {
			t.Fatal("backend detail exposed")
		}
	}
}

func TestTimelineHTTPPassesBothExclusionLists(t *testing.T) {
	r := gin.New()
	r.GET("/api/v1/timeline", timelineHandler(func(_ context.Context, opt timeline.Options) (*timeline.Page, error) {
		if len(opt.ExcludeTalkers) != 2 || opt.ExcludeTalkers[0] != "wxid_a" || opt.ExcludeTalkers[1] != "group@chatroom" || len(opt.ExcludeSenders) != 1 || opt.ExcludeSenders[0] != "wxid_b" {
			t.Fatal("exclusions not passed to query")
		}
		return &timeline.Page{Items: []*timeline.Item{}}, nil
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/timeline?start=2026-11-01&end=2026-11-02&exclude_talker=wxid_a,group@chatroom&exclude_sender=wxid_b", nil))
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
}
