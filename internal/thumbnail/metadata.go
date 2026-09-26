package thumbnail

import (
	"net/url"
	"strings"

	"golang.org/x/net/html"

	"github.com/spool-reader/spool/internal/safehttp"
)

func extractImageURL(pageURL, source string) string {
	base, err := url.Parse(pageURL)
	if err != nil || !safehttp.ValidURL(base) {
		return ""
	}
	document, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return ""
	}

	var twitterImage, openGraphImage string
	nodes := []*html.Node{document}
	for len(nodes) > 0 {
		last := len(nodes) - 1
		node := nodes[last]
		nodes = nodes[:last]
		if node.Type == html.ElementNode && node.Data == "meta" {
			var name, property, content string
			for _, attr := range node.Attr {
				switch strings.ToLower(attr.Key) {
				case "name":
					name = strings.ToLower(strings.TrimSpace(attr.Val))
				case "property":
					property = strings.ToLower(strings.TrimSpace(attr.Val))
				case "content":
					content = strings.TrimSpace(attr.Val)
				}
			}
			if content != "" {
				if (name == "twitter:image" || property == "twitter:image") && twitterImage == "" {
					twitterImage = resolveImageURL(base, content)
				}
				if property == "og:image" && openGraphImage == "" {
					openGraphImage = resolveImageURL(base, content)
				}
			}
		}
		for child := node.LastChild; child != nil; child = child.PrevSibling {
			nodes = append(nodes, child)
		}
	}

	if twitterImage != "" {
		return twitterImage
	}
	return openGraphImage
}

func resolveImageURL(base *url.URL, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	imageURL, err := url.Parse(value)
	if err != nil {
		return ""
	}
	resolved := base.ResolveReference(imageURL)
	if !safehttp.ValidURL(resolved) {
		return ""
	}
	resolved.Fragment = ""
	resolved.RawFragment = ""
	return resolved.String()
}
