package database

import (
	"context"

	"github.com/sjzar/chatlog/internal/wechatdb/timeline"
)

func (s *Service) GetTimeline(ctx context.Context, options timeline.Options) (*timeline.Page, error) {
	if s.conf.GetPlatform() != "darwin" || s.conf.GetVersion() != 3 {
		return nil, timeline.ErrUnsupported
	}
	return timeline.Query(ctx, s.conf.GetWorkDir(), options)
}
