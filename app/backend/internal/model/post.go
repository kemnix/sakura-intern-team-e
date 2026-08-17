package model

import "time"

type Post struct {
	ID             int64     `json:"id"`
	Content        *string   `json:"content"`
	CreatedAt      time.Time `json:"created_at"`
	LikesCount     int       `json:"likes_count"`
	RepostsCount   int       `json:"reposts_count"`
	RepliesCount   int       `json:"replies_count"`
	IsRepost       bool      `json:"is_repost"`
	OriginalPostID *int64    `json:"original_post_id"`
	OriginalPost   *Post     `json:"original_post,omitempty"`
	ParentPostID   *int64    `json:"parent_post_id"`
	// 返信の場合、返信先の投稿者
	ReplyToUsername    *string `json:"reply_to_username,omitempty"`
	ReplyToDisplayName *string `json:"reply_to_display_name,omitempty"`
	Author             User    `json:"author"`
	LikedByMe          bool    `json:"liked_by_me"`
	RepostedByMe       bool    `json:"reposted_by_me"`
}
