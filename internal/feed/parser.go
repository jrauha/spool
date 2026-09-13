package feed

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"
)

type ParsedFeed struct {
	Title       string
	Description string
	SiteURL     string
	IconURL     string
	Items       []ParsedItem
}

type ParsedItem struct {
	GUID        string
	URL         string
	Title       string
	Summary     string
	Author      string
	PublishedAt *time.Time
}

func Parse(r io.Reader) (ParsedFeed, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return ParsedFeed{}, err
	}

	var root struct {
		XMLName xml.Name
	}
	if err := xml.Unmarshal(data, &root); err != nil {
		return ParsedFeed{}, err
	}

	switch root.XMLName.Local {
	case "rss", "RDF":
		return parseRSS(data)
	case "feed":
		return parseAtom(data)
	default:
		return ParsedFeed{}, fmt.Errorf("unsupported feed format %q", root.XMLName.Local)
	}
}

type rssDocument struct {
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string    `xml:"title"`
	Description string    `xml:"description"`
	Links       []rssLink `xml:"link"`
	Image       rssImage  `xml:"image"`
	Items       []rssItem `xml:"item"`
}

type rssLink struct {
	Href  string `xml:"href,attr"`
	Rel   string `xml:"rel,attr"`
	Value string `xml:",chardata"`
}

type rssImage struct {
	URL string `xml:"url"`
}

type rssItem struct {
	GUID        string `xml:"guid"`
	Link        string `xml:"link"`
	Title       string `xml:"title"`
	Description string `xml:"description"`
	Author      string `xml:"author"`
	PublishedAt string `xml:"pubDate"`
}

func parseRSS(data []byte) (ParsedFeed, error) {
	var doc rssDocument
	if err := xml.Unmarshal(data, &doc); err != nil {
		return ParsedFeed{}, err
	}

	feed := ParsedFeed{
		Title:       clean(doc.Channel.Title),
		Description: clean(doc.Channel.Description),
		SiteURL:     rssURL(doc.Channel.Links),
		IconURL:     clean(doc.Channel.Image.URL),
		Items:       make([]ParsedItem, 0, len(doc.Channel.Items)),
	}
	for _, entry := range doc.Channel.Items {
		feed.Items = append(feed.Items, ParsedItem{
			GUID:        clean(entry.GUID),
			URL:         clean(entry.Link),
			Title:       clean(entry.Title),
			Summary:     clean(entry.Description),
			Author:      clean(entry.Author),
			PublishedAt: parseTime(entry.PublishedAt),
		})
	}
	return feed, nil
}

type atomDocument struct {
	Title    string      `xml:"title"`
	Subtitle string      `xml:"subtitle"`
	Links    []atomLink  `xml:"link"`
	Icon     string      `xml:"icon"`
	Entries  []atomEntry `xml:"entry"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
}

type atomEntry struct {
	ID        string     `xml:"id"`
	Title     string     `xml:"title"`
	Summary   string     `xml:"summary"`
	Content   string     `xml:"content"`
	Links     []atomLink `xml:"link"`
	Author    atomAuthor `xml:"author"`
	Published string     `xml:"published"`
	Updated   string     `xml:"updated"`
}

type atomAuthor struct {
	Name string `xml:"name"`
}

func parseAtom(data []byte) (ParsedFeed, error) {
	var doc atomDocument
	if err := xml.Unmarshal(data, &doc); err != nil {
		return ParsedFeed{}, err
	}

	feed := ParsedFeed{
		Title:       clean(doc.Title),
		Description: clean(doc.Subtitle),
		SiteURL:     atomURL(doc.Links),
		IconURL:     clean(doc.Icon),
		Items:       make([]ParsedItem, 0, len(doc.Entries)),
	}
	for _, entry := range doc.Entries {
		summary := clean(entry.Summary)
		if summary == "" {
			summary = clean(entry.Content)
		}
		published := entry.Published
		if clean(published) == "" {
			published = entry.Updated
		}
		feed.Items = append(feed.Items, ParsedItem{
			GUID:        clean(entry.ID),
			URL:         atomURL(entry.Links),
			Title:       clean(entry.Title),
			Summary:     summary,
			Author:      clean(entry.Author.Name),
			PublishedAt: parseTime(published),
		})
	}
	return feed, nil
}

func rssURL(links []rssLink) string {
	for _, link := range links {
		if value := clean(link.Value); value != "" {
			return value
		}
	}
	for _, link := range links {
		if link.Rel == "" || link.Rel == "alternate" {
			return clean(link.Href)
		}
	}
	return ""
}

func atomURL(links []atomLink) string {
	for _, link := range links {
		if link.Rel == "" || link.Rel == "alternate" {
			return clean(link.Href)
		}
	}
	if len(links) == 0 {
		return ""
	}
	return clean(links[0].Href)
}

func parseTime(value string) *time.Time {
	value = clean(value)
	for _, layout := range []string{time.RFC3339, time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return &parsed
		}
	}
	return nil
}

func clean(value string) string {
	return strings.TrimSpace(value)
}
