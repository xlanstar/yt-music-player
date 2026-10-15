package stream

// Client is an InnerTube client the resolver poses as. Which clients get
// direct, unthrottled audio URLs without a PO token or the JS player
// changes every few months; yt-dlp's INNERTUBE_CLIENTS
// (yt_dlp/extractor/youtube/_base.py) tracks them.
type Client struct {
	// Name is the context's clientName, e.g. "VISIONOS".
	Name string
	// ID is the X-YouTube-Client-Name number; Version must be one
	// InnerTube still accepts for Name.
	ID      int
	Version string
	// UserAgent is sent with the player request and kept in Stream.Header.
	UserAgent string

	DeviceMake, DeviceModel string
	OSName, OSVersion       string
	AndroidSDKVersion       int
}

// VisionOS gets full-speed direct audio URLs that need neither a PO token
// nor n-parameter descrambling. It is yt-dlp's only default client without
// a JS runtime. It cannot play "made for kids" videos.
var VisionOS = Client{
	Name:        "VISIONOS",
	ID:          101,
	Version:     "1.02",
	UserAgent:   "Mozilla/5.0 (Macintosh; Intel Mac OS X 15_7_3) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Safari/605.1.15",
	DeviceMake:  "Apple",
	DeviceModel: "RealityDevice17,1",
	OSName:      "visionOS",
	OSVersion:   "26.5.23O471",
}

// AndroidVR was the long-time PO-token-free client. Since August 2026
// googlevideo serves only the first MiB of its URLs, then 403 (the probe
// catches it); it stays as a fallback as that enforcement has come and
// gone before.
var AndroidVR = Client{
	Name:              "ANDROID_VR",
	ID:                28,
	Version:           "1.65.10",
	UserAgent:         "com.google.android.apps.youtube.vr.oculus/1.65.10 (Linux; U; Android 12L; eureka-user Build/SQ3A.220605.009.A1) gzip",
	DeviceMake:        "Oculus",
	DeviceModel:       "Quest 3",
	OSName:            "Android",
	OSVersion:         "12L",
	AndroidSDKVersion: 32,
}

// clientContext is a player request's context. Fields left zero are
// left out, as InnerTube's clients send only what they have.
type clientContext struct {
	Client clientInfo `json:"client"`
}

type clientInfo struct {
	ClientName        string `json:"clientName"`
	ClientVersion     string `json:"clientVersion"`
	HL                string `json:"hl"`
	UserAgent         string `json:"userAgent,omitzero"`
	DeviceMake        string `json:"deviceMake,omitzero"`
	DeviceModel       string `json:"deviceModel,omitzero"`
	OSName            string `json:"osName,omitzero"`
	OSVersion         string `json:"osVersion,omitzero"`
	VisitorData       string `json:"visitorData,omitzero"`
	AndroidSDKVersion int    `json:"androidSdkVersion,omitzero"`
}

func (c Client) context(visitorData string) clientContext {
	return clientContext{Client: clientInfo{
		ClientName: c.Name, ClientVersion: c.Version,
		// hl is pinned to English: bot checks are recognized by their reason.
		HL:        "en",
		UserAgent: c.UserAgent, DeviceMake: c.DeviceMake, DeviceModel: c.DeviceModel,
		OSName: c.OSName, OSVersion: c.OSVersion, VisitorData: visitorData,
		AndroidSDKVersion: c.AndroidSDKVersion,
	}}
}
