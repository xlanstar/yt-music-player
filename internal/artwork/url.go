package artwork

import (
	"net/url"
	"strconv"
	"strings"
)

// buckets are the pixel sizes images are fetched and kept at, so a grid
// resized by a pixel, or one cover at slightly different sizes, shares an
// image. Steps of at most an eighth keep images at most a quarter larger
// than shown.
var buckets = [...]int{64, 80, 96, 112, 128, 160, 192, 224, 256, 288, 320, 352, 384, 448, 512, 576, 640, 768, 896, 1024}

// Bucket returns the size in pixels to fetch an image shown px pixels
// wide at: the next bucket up, or the largest.
func Bucket(px int) int {
	for _, b := range buckets {
		if px <= b {
			return b
		}
	}
	return buckets[len(buckets)-1]
}

// SizedURL returns u resized for px pixels where the host allows: Google's
// image hosts scale to any size, YouTube's video thumbnails come in a few.
// Other URLs come back unchanged.
func SizedURL(u string, px int) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return u
	}
	switch host := parsed.Hostname(); {
	case strings.HasSuffix(host, ".googleusercontent.com") || strings.HasSuffix(host, ".ggpht.com"):
		return googleSized(u, px)
	case host == "i.ytimg.com" || strings.HasSuffix(host, ".ytimg.com") || host == "img.youtube.com":
		end := len(u)
		if i := strings.IndexAny(u, "?#"); i >= 0 {
			end = i
		}
		if sized, ok := ytimgSized(u[:end], px, parsed.RawQuery != ""); ok {
			return sized
		}
	}
	return u
}

// googleSized rewrites the size options after the last '=' of a Google
// image URL, as "=w544-h544-l90-rj" or "=s120", keeping the others (l90 is
// the JPEG quality, rj the format), but those two, which become "rw": WebP
// at the host's default quality, sharper than "l90-rj" and smaller.
func googleSized(u string, px int) string {
	end := len(u)
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		end = i
	}
	base, opts, ok := strings.CutLast(u[:end], "=")
	if !ok || strings.Contains(opts, "/") || !hasSizeOpt(opts) {
		return u
	}
	// One Builder: splitting and joining cost 6 allocations.
	var num [20]byte
	n := strconv.AppendInt(num[:0], int64(px), 10)
	var b strings.Builder
	b.Grow(len(u) + len("w-h-rw") + 2*len(n))
	b.WriteString(base)
	b.WriteString("=w")
	b.Write(n)
	b.WriteString("-h")
	b.Write(n)
	first := true
	for opt := range strings.SplitSeq(opts, "-") {
		if isSizeOpt(opt) || isEncodingOpt(opt) {
			continue
		}
		// Deliberately, an empty first option drops the rest.
		if first && opt == "" {
			break
		}
		first = false
		b.WriteByte('-')
		b.WriteString(opt)
	}
	b.WriteString("-rw")
	b.WriteString(u[end:])
	return b.String()
}

// isSizeOpt reports a size option: w, h or s and a number of pixels.
func isSizeOpt(opt string) bool {
	return len(opt) > 1 && strings.IndexByte("whs", opt[0]) >= 0 && isDigits(opt[1:])
}

// isEncodingOpt reports a format (rj, rp, rw, rg) or quality (l90) option.
func isEncodingOpt(opt string) bool {
	isFormat := len(opt) == 2 && opt[0] == 'r' && strings.IndexByte("jpwg", opt[1]) >= 0
	return isFormat || len(opt) > 1 && opt[0] == 'l' && isDigits(opt[1:])
}

func hasSizeOpt(opts string) bool {
	for opt := range strings.SplitSeq(opts, "-") {
		if isSizeOpt(opt) {
			return true
		}
	}
	return false
}

// ytVariant is a YouTube video thumbnail.
type ytVariant struct {
	name string
	// side is the largest square of the video it shows, in pixels: 4:3
	// ones letterbox a 16:9 video, which Decode trims.
	side int
	// isUniversal is set when every video has it; others may not exist.
	isUniversal bool
}

var ytVariants = [...]ytVariant{
	{name: "default", side: 68, isUniversal: true},
	{name: "mqdefault", side: 180, isUniversal: true},
	{name: "hqdefault", side: 270, isUniversal: true},
	{name: "sddefault", side: 360, isUniversal: false},
	{name: "hq720", side: 720, isUniversal: false},
	{name: "maxresdefault", side: 720, isUniversal: false},
}

// ytimgSized picks the smallest thumbnail that covers px, never one
// larger than the URL's unless every video has it. A signed URL's sqp
// names its crop and rs signs it, so it gives way only to one every video
// has unsigned, which Decode squares anyway.
func ytimgSized(u string, px int, isSigned bool) (string, bool) {
	dir, file, hasDir := strings.CutLast(u, "/")
	name, ext, hasExt := strings.CutLast(file, ".")
	isThumbnail := hasDir && hasExt && strings.Contains(dir, "/vi")
	if !isThumbnail {
		return "", false
	}
	named := -1
	for i, v := range ytVariants {
		if v.name == name {
			named = i
		}
	}
	if named < 0 {
		return "", false
	}
	pick := named
	for i, v := range ytVariants {
		if v.side >= px || i == len(ytVariants)-1 {
			pick = i
			break
		}
	}
	switch {
	case isSigned && !ytVariants[pick].isUniversal:
		return "", false
	case ytVariants[pick].side == ytVariants[named].side:
		pick = named
	case pick > named && !ytVariants[pick].isUniversal:
		// Only the named one, or one every video has, surely exists.
		pick = named
		for i := named; i < len(ytVariants) && ytVariants[i].isUniversal; i++ {
			pick = i
		}
	}
	return dir + "/" + ytVariants[pick].name + "." + ext, true
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
