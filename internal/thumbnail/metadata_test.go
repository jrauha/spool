package thumbnail

import "testing"

func TestExtractImageURL(t *testing.T) {
	tests := []struct {
		name string
		html string
		want string
	}{
		{
			name: "twitter image takes priority",
			html: `<meta property="og:image" content="/open-graph.jpg"><meta name="twitter:image" content="/twitter.jpg">`,
			want: "https://example.com/twitter.jpg",
		},
		{
			name: "skip invalid twitter candidate",
			html: `<meta name="twitter:image" content="javascript:alert(1)"><meta name="twitter:image" content="/valid-twitter.jpg"><meta property="og:image" content="/open-graph.jpg">`,
			want: "https://example.com/valid-twitter.jpg",
		},
		{
			name: "open graph fallback",
			html: `<meta property="og:image" content="https://cdn.example.com/image.jpg">`,
			want: "https://cdn.example.com/image.jpg",
		},
		{
			name: "reject unsafe image URL",
			html: `<meta name="twitter:image" content="javascript:alert(1)"><meta property="og:image" content="//cdn.example.com/image.jpg">`,
			want: "https://cdn.example.com/image.jpg",
		},
		{
			name: "missing image",
			html: `<title>Example</title>`,
			want: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := extractImageURL("https://example.com/articles/one", test.html); got != test.want {
				t.Fatalf("extractImageURL() = %q, want %q", got, test.want)
			}
		})
	}
}
