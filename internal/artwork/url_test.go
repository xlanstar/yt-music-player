package artwork

import "testing"

func TestBucket(t *testing.T) {
	for _, tc := range []struct{ px, want int }{
		{1, 64}, {64, 64}, {65, 80}, {80, 80}, {96, 96}, {112, 112}, {208, 224}, {336, 352}, {600, 640}, {5000, 1024},
	} {
		if got := Bucket(tc.px); got != tc.want {
			t.Errorf("Bucket(%d) = %d, want %d", tc.px, got, tc.want)
		}
	}
}

func TestSizedURL(t *testing.T) {
	const lh3 = "https://lh3.googleusercontent.com/abc_DEF-123"
	const yt3 = "https://yt3.ggpht.com/xyz"
	const vi = "https://i.ytimg.com/vi/ZXhwXhv37Ps/"
	for _, tc := range []struct {
		in   string
		px   int
		want string
	}{
		{lh3 + "=w544-h544-l90-rj", 192, lh3 + "=w192-h192-l90-rj"},
		{lh3 + "=w60-h60-l90-rj", 1024, lh3 + "=w1024-h1024-l90-rj"},
		{lh3 + "=w60-h60", 96, lh3 + "=w96-h96"},
		{yt3 + "=s120-c-k-c0x00ffffff-no-rj", 256, yt3 + "=w256-h256-c-k-c0x00ffffff-no-rj"},
		{lh3 + "=w60-h60-l90-rj?x=1", 96, lh3 + "=w96-h96-l90-rj?x=1"},
		// Nothing to resize: an options part without a size, or none.
		{lh3 + "=l90-rj", 96, lh3 + "=l90-rj"},
		{lh3, 96, lh3},
		{"https://lh3.googleusercontent.com/a=b/c", 96, "https://lh3.googleusercontent.com/a=b/c"},
		// Video thumbnails: the smallest that covers the size, never one
		// that may not exist.
		{vi + "hqdefault.jpg", 96, vi + "mqdefault.jpg"},
		{vi + "hqdefault.jpg", 256, vi + "hqdefault.jpg"},
		{vi + "hqdefault.jpg", 640, vi + "hqdefault.jpg"},
		{vi + "default.jpg", 640, vi + "hqdefault.jpg"},
		{vi + "maxresdefault.jpg", 640, vi + "maxresdefault.jpg"},
		{vi + "maxresdefault.jpg", 320, vi + "sddefault.jpg"},
		{vi + "sddefault.jpg", 128, vi + "mqdefault.jpg"},
		{"https://i.ytimg.com/vi_webp/ZXhwXhv37Ps/hqdefault.webp", 96, "https://i.ytimg.com/vi_webp/ZXhwXhv37Ps/mqdefault.webp"},
		// Signed crops stay as they are.
		{vi + "hqdefault.jpg?sqp=-oaym&rs=AMz", 96, vi + "hqdefault.jpg?sqp=-oaym&rs=AMz"},
		{vi + "frame0.jpg", 96, vi + "frame0.jpg"},
		{"https://example.com/a=w60-h60", 96, "https://example.com/a=w60-h60"},
		{"neon-tide", 96, "neon-tide"},
	} {
		if got := SizedURL(tc.in, tc.px); got != tc.want {
			t.Errorf("SizedURL(%q, %d)\n got %q\nwant %q", tc.in, tc.px, got, tc.want)
		}
	}
}
