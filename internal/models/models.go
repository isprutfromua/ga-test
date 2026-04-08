package models

import "time"

type Subscription struct {
	ID               int64     `json:"-"`
	Email            string    `json:"email"`
	Repo             string    `json:"repo"`
	Confirmed        bool      `json:"confirmed"`
	LastSeenTag      string    `json:"last_seen_tag,omitempty"`
	ConfirmToken     string    `json:"-"`
	UnsubscribeToken string    `json:"-"`
	CreatedAt        time.Time `json:"-"`
	UpdatedAt        time.Time `json:"-"`
}

type GitHubRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
}
