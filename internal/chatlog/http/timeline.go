package http

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/sjzar/chatlog/internal/wechatdb/timeline"
)

type timelineQuery func(context.Context, timeline.Options) (*timeline.Page, error)

func (s *Service) handleTimeline(c *gin.Context) {
	timelineHandler(s.db.GetTimeline)(c)
}

func timelineHandler(query timelineQuery) gin.HandlerFunc {
	return func(c *gin.Context) {
		for _, name := range []string{"exclude_talker", "exclude_sender"} {
			if len(c.Request.URL.Query()[name]) > 1 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "use one comma-separated value per exclusion parameter"})
				return
			}
		}
		if format := c.Query("format"); format != "" && format != "json" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "timeline supports JSON only"})
			return
		}
		start, end, err := timeline.ParseRange(c.Query("start"), c.Query("end"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "valid start and end are required; dates use Beijing time and end is exclusive"})
			return
		}
		limit := timeline.DefaultLimit
		if value, ok := c.GetQuery("limit"); ok {
			limit, err = strconv.Atoi(value)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid limit"})
				return
			}
		}
		options := timeline.Options{Start: start, End: end, Limit: limit, Cursor: c.Query("cursor"),
			ExcludeTalkers: strings.Split(c.Query("exclude_talker"), ","), ExcludeSenders: strings.Split(c.Query("exclude_sender"), ",")}
		if err := timeline.Validate(options); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid pagination, exclusions, or cursor range/filter mismatch"})
			return
		}
		page, err := query(c.Request.Context(), options)
		if errors.Is(err, timeline.ErrUnsupported) {
			c.JSON(http.StatusNotImplemented, gin.H{"error": err.Error()})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "timeline query failed; no partial result returned"})
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, page)
	}
}
