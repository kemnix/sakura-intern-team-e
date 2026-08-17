package model

import "time"

type User struct {
	ID             int64     `json:"id"`
	Username       string    `json:"username"`
	DisplayName    string    `json:"display_name"`
	Bio            *string   `json:"bio,omitempty"`
	FollowersCount int       `json:"followers_count"`
	FollowingCount int       `json:"following_count"`
	PostCount      int       `json:"post_count"`
	AvatarColor    string    `json:"avatar_color"`
	FollowedByMe   bool      `json:"followed_by_me"`
	CreatedAt      time.Time `json:"created_at"`
}

var avatarColors = []string{"orange", "blue", "purple", "mint", "pink"}

func AvatarColor(id int64) string {
	return avatarColors[id%5]
}
