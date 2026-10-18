package ytmusic

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"strings"
	"yt-music-player/internal/catalog"
)

// response holds what the parsers read of any InnerTube response; each
// endpoint fills its own part.
type response struct {
	Contents *contents `json:"contents"`
	Header   struct {
		Detail *header `json:"musicDetailHeaderRenderer"`
	} `json:"header"`
	ResponseContext responseContext `json:"responseContext"`
	Alerts          []struct {
		Alert *alertRenderer `json:"alertRenderer"`
	} `json:"alerts"`
	ContinuationContents struct {
		SectionList *sectionListRenderer `json:"sectionListContinuation"`
		Grid        *shelf               `json:"gridContinuation"`
		Shelf       *shelf               `json:"musicShelfContinuation"`
	} `json:"continuationContents"`
	ReceivedActions []struct {
		Append struct {
			Items []item `json:"continuationItems"`
		} `json:"appendContinuationItemsAction"`
	} `json:"onResponseReceivedActions"`
	Actions []struct {
		OpenPopup struct {
			Popup struct {
				Menu struct {
					Header struct {
						Account *accountHeader `json:"activeAccountHeaderRenderer"`
					} `json:"header"`
				} `json:"multiPageMenuRenderer"`
			} `json:"popup"`
		} `json:"openPopupAction"`
	} `json:"actions"`
}

type responseContext struct {
	VisitorData           string `json:"visitorData"`
	ServiceTrackingParams []struct {
		Params []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"params"`
	} `json:"serviceTrackingParams"`
}

type alertRenderer struct {
	Type string `json:"type"`
	Text text   `json:"text"`
}

type accountHeader struct {
	AccountName   text       `json:"accountName"`
	ChannelHandle text       `json:"channelHandle"`
	AccountPhoto  thumbnails `json:"accountPhoto"`
}

type contents struct {
	TwoColumn struct {
		tabs
		Secondary struct {
			SectionList *sectionListRenderer `json:"sectionListRenderer"`
		} `json:"secondaryContents"`
	} `json:"twoColumnBrowseResultsRenderer"`
	SingleColumn tabs                 `json:"singleColumnBrowseResultsRenderer"`
	TabbedSearch tabs                 `json:"tabbedSearchResultsRenderer"`
	SectionList  *sectionListRenderer `json:"sectionListRenderer"`
}

type tabs struct {
	Tabs []struct {
		Renderer struct {
			Content struct {
				SectionList *sectionListRenderer `json:"sectionListRenderer"`
			} `json:"content"`
		} `json:"tabRenderer"`
	} `json:"tabs"`
}

func (t *tabs) first() *sectionListRenderer {
	if len(t.Tabs) == 0 {
		return nil
	}
	return t.Tabs[0].Renderer.Content.SectionList
}

type sectionListRenderer struct {
	Contents      []section     `json:"contents"`
	Continuations continuations `json:"continuations"`
}

func (l *sectionListRenderer) sections() []section {
	if l == nil {
		return nil
	}
	return l.Contents
}

func (l *sectionListRenderer) next() string {
	if l == nil {
		return ""
	}
	return l.Continuations.next()
}

type continuations []struct {
	Next struct {
		Token string `json:"continuation"`
	} `json:"nextContinuationData"`
}

// next is the token of the next page, in the older form feeds and
// library grids still use.
func (c continuations) next() string {
	if len(c) == 0 {
		return ""
	}
	return c[0].Next.Token
}

type section struct {
	Shelf         *shelf `json:"musicShelfRenderer"`
	Carousel      *shelf `json:"musicCarouselShelfRenderer"`
	PlaylistShelf *shelf `json:"musicPlaylistShelfRenderer"`
	Grid          *shelf `json:"gridRenderer"`
	ItemSection   *struct {
		Contents []section `json:"contents"`
	} `json:"itemSectionRenderer"`
	Header         *header `json:"musicResponsiveHeaderRenderer"`
	EditableHeader struct {
		Header struct {
			Header *header `json:"musicResponsiveHeaderRenderer"`
		} `json:"header"`
	} `json:"musicEditablePlaylistDetailHeaderRenderer"`
	Message struct {
		Button struct {
			Renderer struct {
				Endpoint struct {
					SignIn *struct{} `json:"signInEndpoint"`
				} `json:"navigationEndpoint"`
			} `json:"buttonRenderer"`
		} `json:"button"`
	} `json:"messageRenderer"`
}

// shelf is any list of items: a shelf, carousel or grid.
type shelf struct {
	Header struct {
		Basic struct {
			Title     text `json:"title"`
			Strapline text `json:"strapline"`
		} `json:"musicCarouselShelfBasicHeaderRenderer"`
	} `json:"header"`
	Contents      []item        `json:"contents"`
	Items         []item        `json:"items"`
	Continuations continuations `json:"continuations"`
}

func (s *shelf) rows() []item {
	if s == nil {
		return nil
	}
	if s.Items != nil {
		return s.Items
	}
	return s.Contents
}

func (s *shelf) next() string {
	if s == nil {
		return ""
	}
	return s.Continuations.next()
}

type item struct {
	ListItem     *listItem         `json:"musicResponsiveListItemRenderer"`
	Card         *card             `json:"musicTwoRowItemRenderer"`
	Continuation *continuationItem `json:"continuationItemRenderer"`
	MoodButton   *moodButton       `json:"musicNavigationButtonRenderer"`
}

type listItem struct {
	artwork
	DisplayPolicy string `json:"musicItemRendererDisplayPolicy"`
	Overlay       struct {
		Renderer struct {
			Content struct {
				Play struct {
					Endpoint endpoint `json:"playNavigationEndpoint"`
				} `json:"musicPlayButtonRenderer"`
			} `json:"content"`
		} `json:"musicItemThumbnailOverlayRenderer"`
	} `json:"overlay"`
	FlexColumns []struct {
		Renderer struct {
			Text text `json:"text"`
		} `json:"musicResponsiveListItemFlexColumnRenderer"`
	} `json:"flexColumns"`
	FixedColumns []struct {
		Renderer struct {
			Text text `json:"text"`
		} `json:"musicResponsiveListItemFixedColumnRenderer"`
	} `json:"fixedColumns"`
	CustomIndexColumn struct {
		Renderer struct {
			Text text `json:"text"`
		} `json:"musicCustomIndexColumnRenderer"`
	} `json:"customIndexColumn"`
	PlaylistItemData   watchEndpoint `json:"playlistItemData"`
	NavigationEndpoint endpoint      `json:"navigationEndpoint"`
}

func (li *listItem) column(i int) runs {
	if li == nil || i >= len(li.FlexColumns) {
		return nil
	}
	return li.FlexColumns[i].Renderer.Text.Runs
}

type card struct {
	artwork
	Title              text     `json:"title"`
	Subtitle           text     `json:"subtitle"`
	NavigationEndpoint endpoint `json:"navigationEndpoint"`
}

type header struct {
	artwork
	Title            text `json:"title"`
	Subtitle         text `json:"subtitle"`
	StraplineTextOne text `json:"straplineTextOne"`
	Facepile         struct {
		Stack struct {
			Text struct {
				Content string `json:"content"`
			} `json:"text"`
		} `json:"avatarStackViewModel"`
	} `json:"facepile"`
}

type moodButton struct {
	ButtonText   text     `json:"buttonText"`
	ClickCommand endpoint `json:"clickCommand"`
	Solid        struct {
		LeftStripeColor float64 `json:"leftStripeColor"`
	} `json:"solid"`
}

type continuationItem struct {
	Endpoint struct {
		Command  *continuationCommand `json:"continuationCommand"`
		Executor struct {
			Commands []struct {
				Command *continuationCommand `json:"continuationCommand"`
			} `json:"commands"`
		} `json:"commandExecutorCommand"`
	} `json:"continuationEndpoint"`
}

type continuationCommand struct {
	Token   string `json:"token"`
	Request string `json:"request"`
}

type endpoint struct {
	Browse        *browseEndpoint `json:"browseEndpoint"`
	Watch         watchEndpoint   `json:"watchEndpoint"`
	WatchPlaylist struct {
		PlaylistID string `json:"playlistId"`
	} `json:"watchPlaylistEndpoint"`
}

type watchEndpoint struct {
	VideoID string `json:"videoId"`
}

type browseEndpoint struct {
	ID      string `json:"browseId"`
	Params  string `json:"params"`
	Configs struct {
		Music struct {
			PageType string `json:"pageType"`
		} `json:"browseEndpointContextMusicConfig"`
	} `json:"browseEndpointContextSupportedConfigs"`
}

// target is the ID and type of the page the endpoint opens.
func (b *browseEndpoint) target() (id, page string) {
	if b == nil {
		return "", ""
	}
	return b.ID, b.Configs.Music.PageType
}

type run struct {
	Text               string    `json:"text"`
	NavigationEndpoint *endpoint `json:"navigationEndpoint"`
}

type runs []run

func (rs runs) String() string {
	// Most texts are one run, which needs no copy.
	if len(rs) == 1 {
		return rs[0].Text
	}
	var b strings.Builder
	for _, r := range rs {
		b.WriteString(r.Text)
	}
	return b.String()
}

type text struct {
	Runs       runs   `json:"runs"`
	SimpleText string `json:"simpleText"`
}

func (t *text) String() string {
	if s := t.Runs.String(); s != "" {
		return s
	}
	return t.SimpleText
}

type thumbnails struct {
	Thumbnails []struct {
		URL    string  `json:"url"`
		Width  float64 `json:"width"`
		Height float64 `json:"height"`
	} `json:"thumbnails"`
}

// largest is the URL of the largest thumbnail; of equals, the last, as
// YouTube lists them in growing size.
func (t *thumbnails) largest() string {
	var best string
	bestArea := -1.0
	for _, th := range t.Thumbnails {
		if area := th.Width * th.Height; th.URL != "" && area >= bestArea {
			best, bestArea = th.URL, area
		}
	}
	return best
}

type thumbnailRenderers struct {
	Music struct {
		Thumbnail thumbnails `json:"thumbnail"`
	} `json:"musicThumbnailRenderer"`
	Cropped struct {
		Thumbnail thumbnails `json:"thumbnail"`
	} `json:"croppedSquareThumbnailRenderer"`
}

type artwork struct {
	Thumbnail         thumbnailRenderers `json:"thumbnail"`
	ThumbnailRenderer thumbnailRenderers `json:"thumbnailRenderer"`
}

// art picks the largest thumbnail, wherever the renderer keeps them.
func (a *artwork) art() catalog.Art {
	for _, t := range [...]*thumbnails{
		&a.Thumbnail.Music.Thumbnail, &a.Thumbnail.Cropped.Thumbnail,
		&a.ThumbnailRenderer.Music.Thumbnail,
	} {
		if best := t.largest(); best != "" {
			return catalog.Art(best)
		}
	}
	return ""
}

// lenient skips a string or number of another kind, so that a changed
// field reads as missing rather than failing the response.
var lenient = json.WithUnmarshalers(json.JoinUnmarshalers(
	json.UnmarshalFromFunc(func(dec *jsontext.Decoder, _ *string) error { return skipUnless(dec, '"') }),
	json.UnmarshalFromFunc(func(dec *jsontext.Decoder, _ *float64) error { return skipUnless(dec, '0') }),
))

func skipUnless(dec *jsontext.Decoder, want jsontext.Kind) error {
	if k := dec.PeekKind(); k == want || k == 'n' || k == jsontext.KindInvalid {
		return errors.ErrUnsupported
	}
	return dec.SkipValue()
}

func decode(r io.Reader) (*response, error) {
	var resp response
	if err := json.UnmarshalRead(r, &resp, lenient); err != nil {
		return nil, err
	}
	return &resp, nil
}
