// Copyright 2017 Frédéric Guillot. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package rss // import "miniflux.app/reader/rss"

import (
	"encoding/xml"
	"path"
	"strconv"
	"strings"
	"time"

	"miniflux.app/crypto"
	"miniflux.app/logger"
	"miniflux.app/model"
	"miniflux.app/reader/date"
	"miniflux.app/reader/media"
	"miniflux.app/reader/sanitizer"
	"miniflux.app/url"
)

type rssFeed struct {
	XMLName      xml.Name  `xml:"rss"`
	Version      string    `xml:"version,attr"`
	Title        string    `xml:"channel>title"`
	Links        []rssLink `xml:"channel>link"`
	Language     string    `xml:"channel>language"`
	Description  string    `xml:"channel>description"`
	PubDate      string    `xml:"channel>pubDate"`
	ItunesAuthor string    `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd channel>author"`
	Items        []rssItem `xml:"channel>item"`
}

type rssLink struct {
	XMLName xml.Name
	Data    string `xml:",chardata"`
	Href    string `xml:"href,attr"`
	Rel     string `xml:"rel,attr"`
}

type rssCommentLink struct {
	XMLName xml.Name
	Data    string `xml:",chardata"`
}

type rssAuthor struct {
	XMLName xml.Name
	Data    string `xml:",chardata"`
	Name    string `xml:"name"`
	Inner   string `xml:",innerxml"`
}

type rssEnclosure struct {
	URL    string `xml:"url,attr"`
	Type   string `xml:"type,attr"`
	Length string `xml:"length,attr"`
}

func (enclosure *rssEnclosure) Size() int64 {
	if enclosure.Length == "" {
		return 0
	}
	size, _ := strconv.ParseInt(enclosure.Length, 10, 0)
	return size
}

type rssItem struct {
	GUID              string           `xml:"guid"`
	Title             string           `xml:"title"`
	Links             []rssLink        `xml:"link"`
	OriginalLink      string           `xml:"http://rssnamespace.org/feedburner/ext/1.0 origLink"`
	CommentLinks      []rssCommentLink `xml:"comments"`
	Description       string           `xml:"description"`
	EncodedContent    string           `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
	PubDate           string           `xml:"pubDate"`
	Date              string           `xml:"http://purl.org/dc/elements/1.1/ date"`
	Authors           []rssAuthor      `xml:"author"`
	Creator           string           `xml:"http://purl.org/dc/elements/1.1/ creator"`
	EnclosureLinks    []rssEnclosure   `xml:"enclosure"`
	OrigEnclosureLink string           `xml:"http://rssnamespace.org/feedburner/ext/1.0 origEnclosureLink"`
	media.Element
}

func (r *rssFeed) SiteURL() string {
	for _, element := range r.Links {
		if element.XMLName.Space == "" {
			return strings.TrimSpace(element.Data)
		}
	}

	return ""
}

func (r *rssFeed) FeedURL() string {
	for _, element := range r.Links {
		if element.XMLName.Space == "http://www.w3.org/2005/Atom" {
			return strings.TrimSpace(element.Href)
		}
	}

	return ""
}

func (r *rssFeed) Transform() *model.Feed {
	feed := new(model.Feed)
	feed.SiteURL = r.SiteURL()
	feed.FeedURL = r.FeedURL()
	feed.Title = strings.TrimSpace(r.Title)

	if feed.Title == "" {
		feed.Title = feed.SiteURL
	}

	for _, item := range r.Items {
		entry := item.Transform()

		if entry.Author == "" && r.ItunesAuthor != "" {
			entry.Author = r.ItunesAuthor
		}
		entry.Author = strings.TrimSpace(sanitizer.StripTags(entry.Author))

		if entry.URL == "" {
			entry.URL = feed.SiteURL
		} else {
			entryURL, err := url.AbsoluteURL(feed.SiteURL, entry.URL)
			if err == nil {
				entry.URL = entryURL
			}
		}

		if entry.Title == "" {
			entry.Title = entry.URL
		}

		feed.Entries = append(feed.Entries, entry)
	}

	return feed
}

func (r *rssItem) PublishedDate() time.Time {
	value := r.PubDate
	if r.Date != "" {
		value = r.Date
	}

	if value != "" {
		result, err := date.Parse(value)
		if err != nil {
			logger.Error("rss: %v", err)
			return time.Now()
		}

		return result
	}

	return time.Now()
}

func (r *rssItem) Author() string {
	for _, element := range r.Authors {
		if element.Name != "" {
			return element.Name
		}

		if element.Inner != "" {
			return element.Inner
		}
	}

	return r.Creator
}

func (r *rssItem) Hash() string {
	for _, value := range []string{r.GUID, r.URL()} {
		if value != "" {
			return crypto.Hash(value)
		}
	}

	return ""
}

func (r *rssItem) Content() string {
	if r.EncodedContent != "" {
		return r.EncodedContent
	}

	return r.Description
}

func (r *rssItem) URL() string {
	if r.OriginalLink != "" {
		return r.OriginalLink
	}

	for _, link := range r.Links {
		if link.XMLName.Space == "http://www.w3.org/2005/Atom" && link.Href != "" && isValidLinkRelation(link.Rel) {
			return strings.TrimSpace(link.Href)
		}

		if link.Data != "" {
			return strings.TrimSpace(link.Data)
		}
	}

	return ""
}

func (r *rssItem) Enclosures() model.EnclosureList {
	enclosures := make(model.EnclosureList, 0)
	duplicates := make(map[string]bool, 0)

	for _, mediaThumbnail := range r.AllMediaThumbnails() {
		if _, found := duplicates[mediaThumbnail.URL]; !found {
			duplicates[mediaThumbnail.URL] = true
			enclosures = append(enclosures, &model.Enclosure{
				URL:      mediaThumbnail.URL,
				MimeType: mediaThumbnail.MimeType(),
				Size:     mediaThumbnail.Size(),
			})
		}
	}

	for _, enclosure := range r.EnclosureLinks {
		enclosureURL := enclosure.URL

		if r.OrigEnclosureLink != "" {
			filename := path.Base(r.OrigEnclosureLink)
			if strings.Contains(enclosureURL, filename) {
				enclosureURL = r.OrigEnclosureLink
			}
		}

		if _, found := duplicates[enclosureURL]; !found {
			duplicates[enclosureURL] = true

			enclosures = append(enclosures, &model.Enclosure{
				URL:      enclosureURL,
				MimeType: enclosure.Type,
				Size:     enclosure.Size(),
			})
		}
	}

	for _, mediaContent := range r.AllMediaContents() {
		if _, found := duplicates[mediaContent.URL]; !found {
			duplicates[mediaContent.URL] = true
			enclosures = append(enclosures, &model.Enclosure{
				URL:      mediaContent.URL,
				MimeType: mediaContent.MimeType(),
				Size:     mediaContent.Size(),
			})
		}
	}

	for _, mediaPeerLink := range r.AllMediaPeerLinks() {
		if _, found := duplicates[mediaPeerLink.URL]; !found {
			duplicates[mediaPeerLink.URL] = true
			enclosures = append(enclosures, &model.Enclosure{
				URL:      mediaPeerLink.URL,
				MimeType: mediaPeerLink.MimeType(),
				Size:     mediaPeerLink.Size(),
			})
		}
	}

	return enclosures
}

func (r *rssItem) CommentsURL() string {
	for _, commentLink := range r.CommentLinks {
		if commentLink.XMLName.Space == "" {
			return strings.TrimSpace(commentLink.Data)
		}
	}

	return ""
}

func (r *rssItem) Transform() *model.Entry {
	entry := new(model.Entry)
	entry.URL = r.URL()
	entry.CommentsURL = r.CommentsURL()
	entry.Date = r.PublishedDate()
	entry.Author = r.Author()
	entry.Hash = r.Hash()
	entry.Content = r.Content()
	entry.Title = strings.TrimSpace(r.Title)
	entry.Enclosures = r.Enclosures()
	return entry
}

func isValidLinkRelation(rel string) bool {
	switch rel {
	case "", "alternate", "enclosure", "related", "self", "via":
		return true
	default:
		if strings.HasPrefix(rel, "http") {
			return true
		}
		return false
	}
}
