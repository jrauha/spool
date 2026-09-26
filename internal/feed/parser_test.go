package feed

import (
	"strings"
	"testing"
	"time"
)

func TestParseRSS(t *testing.T) {
	parsed, err := Parse(strings.NewReader(`<?xml version="1.0"?>
<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom"><channel>
  <title>Example</title><description>News</description><link>https://example.com</link><atom:link href="https://example.com/rss" rel="self"/>
  <image><url>https://example.com/favicon.ico</url></image>
  <item><guid>one</guid><title>First</title><link>https://example.com/one</link><enclosure url="https://example.com/one/image.jpg" type="image/jpeg" length="42"/>
    <description>Summary</description><author>Author</author><pubDate>Mon, 02 Jan 2006 15:04:05 MST</pubDate>
  </item>
</channel></rss>`))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Title != "Example" || parsed.SiteURL != "https://example.com" || parsed.IconURL != "https://example.com/favicon.ico" {
		t.Fatalf("feed = %#v", parsed)
	}
	if len(parsed.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(parsed.Items))
	}
	item := parsed.Items[0]
	if item.GUID != "one" || item.Title != "First" || item.URL != "https://example.com/one" || item.ImageURL != "https://example.com/one/image.jpg" {
		t.Fatalf("item = %#v", item)
	}
	if item.PublishedAt == nil || !item.PublishedAt.Equal(time.Date(2006, time.January, 2, 15, 4, 5, 0, time.UTC)) {
		t.Fatalf("published at = %v", item.PublishedAt)
	}
}

func TestParseRSSMediaThumbnail(t *testing.T) {
	parsed, err := Parse(strings.NewReader(`<rss xmlns:media="http://search.yahoo.com/mrss/"><channel>
		<item><guid>one</guid><title>First</title><link>https://example.com/one</link><media:thumbnail url="https://cdn.example.com/one.jpg"/></item>
	</channel></rss>`))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(parsed.Items) != 1 || parsed.Items[0].ImageURL != "https://cdn.example.com/one.jpg" {
		t.Fatalf("items = %#v", parsed.Items)
	}
}

func TestParseAtom(t *testing.T) {
	parsed, err := Parse(strings.NewReader(`<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Example</title><subtitle>News</subtitle><link rel="alternate" href="https://example.com"/><icon>https://example.com/icon.png</icon>
  <entry><id>one</id><title>First</title><link href="https://example.com/one"/><link rel="enclosure" type="image/jpeg" href="https://example.com/one/image.jpg"/>
    <summary>Summary</summary><author><name>Author</name></author><published>2006-01-02T15:04:05Z</published>
  </entry>
</feed>`))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.Title != "Example" || parsed.Description != "News" || parsed.SiteURL != "https://example.com" || parsed.IconURL != "https://example.com/icon.png" {
		t.Fatalf("feed = %#v", parsed)
	}
	if len(parsed.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(parsed.Items))
	}
	item := parsed.Items[0]
	if item.GUID != "one" || item.Author != "Author" || item.ImageURL != "https://example.com/one/image.jpg" {
		t.Fatalf("item = %#v", item)
	}
}
