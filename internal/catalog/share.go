package catalog

// ShareStrings has songs, edited in place, hold one copy of each artist,
// album and cover they repeat: decoded page by page or frame by frame, each
// holds its own, which those kept long (ytmd's queue, a front-end's likes)
// should share.
func ShareStrings(songs []Song) {
	// One song repeats nothing of another.
	if len(songs) < 2 {
		return
	}
	shared := sharedStrings{size: len(songs)}
	for i := range songs {
		song := &songs[i]
		song.Artist, song.Album, song.AlbumID = shared.of(song.Artist), shared.of(song.Album), shared.of(song.AlbumID)
		song.Art = Art(shared.of(string(song.Art)))
	}
}

// sharedStrings hands out the first copy it was given of each string.
type sharedStrings struct {
	first map[string]string
	// size is about how many distinct strings to expect.
	size int
}

func (s *sharedStrings) of(v string) string {
	if v == "" {
		return v
	}
	if s.first == nil {
		s.first = make(map[string]string, s.size)
	}
	if first, ok := s.first[v]; ok {
		return first
	}
	s.first[v] = v
	return v
}

// ShareIDs has ids, edited in place, hold their songs' ID strings, where
// songs describe them in their order, some maybe not, as a playlist's
// details do: a long list then keeps one copy of each. A walk, not a map:
// songs out of order share less, never wrongly.
func ShareIDs(ids []string, songs []Song) {
	j := 0
	for i, id := range ids {
		if j < len(songs) && id == songs[j].ID {
			ids[i] = songs[j].ID
			j++
		}
	}
}
