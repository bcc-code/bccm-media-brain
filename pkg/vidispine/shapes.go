package vidispine

import (
	"context"
	"net/url"
	"slices"
	"strings"
)

// Shape tags that Vidispine and Portal create themselves. Installations add their own.
const (
	ShapeTagOriginal          = "original"
	ShapeTagLowres            = "lowres"
	ShapeTagLowresWatermarked = "lowres_watermarked"
	ShapeTagLowAudio          = "lowaudio"
)

// FileStateClosed is the state of a file that is completely written.
const FileStateClosed = "CLOSED"

func (c *Client) GetShapes(ctx context.Context, itemID string) (*ShapeResult, error) {
	out := &ShapeResult{}
	return out, c.get(ctx, out, url.Values{"content": {"shape"}, "terse": {"true"}}, "item", itemID)
}

type ShapeResult struct {
	Shape []Shape `json:"shape"`
	ID    string  `json:"id"`
}

// GetShape returns the first shape carrying the tag, or nil.
func (sr *ShapeResult) GetShape(tag string) *Shape {
	for i := range sr.Shape {
		if slices.Contains(sr.Shape[i].Tag, tag) {
			return &sr.Shape[i]
		}
	}
	return nil
}

type Shape struct {
	ID                 string             `json:"id"`
	Created            string             `json:"created"`
	EssenceVersion     int                `json:"essenceVersion"`
	Tag                []string           `json:"tag"`
	MimeType           []string           `json:"mimeType"`
	ContainerComponent ContainerComponent `json:"containerComponent"`
	AudioComponent     []AudioComponent   `json:"audioComponent"`
	VideoComponent     []VideoComponent   `json:"videoComponent"`
	BinaryComponent    []BinaryComponent  `json:"binaryComponent"`
	Metadata           KV                 `json:"metadata"`
}

// File returns the shape's file: the container's when there is one, otherwise the first file of
// a binary component (sidecars such as subtitles have no container). Nil when there is none.
func (s *Shape) File() *File {
	if len(s.ContainerComponent.File) > 0 {
		return &s.ContainerComponent.File[0]
	}
	for i := range s.BinaryComponent {
		if len(s.BinaryComponent[i].File) > 0 {
			return &s.BinaryComponent[i].File[0]
		}
	}
	return nil
}

// GetPath returns the absolute path of the shape's file on the Vidispine server, or "" when it
// has no file:// URI.
func (s *Shape) GetPath() string {
	if f := s.File(); f != nil {
		return f.LocalPath()
	}
	return ""
}

type File struct {
	ID          string   `json:"id"`
	Path        string   `json:"path"`
	URI         []string `json:"uri"`
	State       string   `json:"state"`
	Size        int64    `json:"size"`
	Hash        string   `json:"hash"`
	Timestamp   string   `json:"timestamp"`
	RefreshFlag int      `json:"refreshFlag"`
	Storage     string   `json:"storage"`
	Metadata    KV       `json:"metadata"`
	Items       []Item   `json:"item,omitempty"`
}

// LocalPath returns the path of the file's first file:// URI, unescaped, or "".
func (f *File) LocalPath() string {
	for _, raw := range f.URI {
		rest, ok := strings.CutPrefix(raw, "file://")
		if !ok {
			continue
		}
		if p, err := url.PathUnescape(rest); err == nil {
			return p
		}
		return rest
	}
	return ""
}

type KV struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type Item struct {
	ID    string  `json:"id,omitempty"`
	Shape []Shape `json:"shape,omitempty"`
}

type Resolution struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type Fraction struct {
	Numerator   int `json:"numerator"`
	Denominator int `json:"denominator"`
}

// Timestamp is a number of samples in a time base, e.g. 90000 samples of 1/25 s.
type Timestamp struct {
	Samples  int64    `json:"samples"`
	TimeBase Fraction `json:"timeBase"`
}

// Seconds returns the timestamp in seconds, and false when it has no usable time base.
func (t Timestamp) Seconds() (float64, bool) {
	if t.TimeBase.Numerator <= 0 || t.TimeBase.Denominator <= 0 {
		return 0, false
	}
	return float64(t.Samples) * float64(t.TimeBase.Numerator) / float64(t.TimeBase.Denominator), true
}

type ContainerSAR struct {
	Horizontal int `json:"horizontal"`
	Vertical   int `json:"vertical"`
}

type MediaInfo struct {
	FormatSettingsGOP string `json:"Format_Settings_GOP"`
	IntraDcPrecision  int    `json:"intra_dc_precision"`
	Property          []KV   `json:"property"`
	BitRateMode       string `json:"Bit_rate_mode"`
}

type ContainerComponent struct {
	Duration           Timestamp `json:"duration"`
	Format             string    `json:"format"`
	FirstSMPTETimecode string    `json:"firstSMPTETimecode"`
	StartTimecode      int       `json:"startTimecode"`
	StartTimestamp     Timestamp `json:"startTimestamp"`
	RoundedTimeBase    int       `json:"roundedTimeBase"`
	DropFrame          bool      `json:"dropFrame"`
	TimeCodeTimeBase   Fraction  `json:"timeCodeTimeBase"`
	MediaInfo          MediaInfo `json:"mediaInfo"`
	File               []File    `json:"file"`
	ID                 string    `json:"id"`
	Metadata           []KV      `json:"metadata"`
}

type VideoComponent struct {
	Resolution           Resolution   `json:"resolution"`
	PixelFormat          string       `json:"pixelFormat"`
	MaxBFrames           int          `json:"maxBFrames"`
	PixelAspectRatio     Fraction     `json:"pixelAspectRatio"`
	FieldOrder           string       `json:"fieldOrder"`
	CodecTimeBase        Fraction     `json:"codecTimeBase"`
	AverageFrameRate     Fraction     `json:"averageFrameRate"`
	RealBaseFrameRate    Fraction     `json:"realBaseFrameRate"`
	DisplayWidth         Fraction     `json:"displayWidth"`
	DisplayHeight        Fraction     `json:"displayHeight"`
	ContainerSAR         ContainerSAR `json:"containerSAR"`
	ColrPrimaries        int          `json:"colr_primaries"`
	ColrTransferFunction int          `json:"colr_transfer_function"`
	ColrMatrix           int          `json:"colr_matrix"`
	StartTimecode        int          `json:"startTimecode"`
	DropFrame            bool         `json:"dropFrame"`
	BitDepth             int          `json:"bitDepth"`
	BitsPerPixel         int          `json:"bitsPerPixel"`
	ColorPrimaries       string       `json:"colorPrimaries"`
	MediaInfo            MediaInfo    `json:"mediaInfo"`
	File                 []File       `json:"file"`
	ID                   string       `json:"id"`
	Metadata             []KV         `json:"metadata"`
	Codec                string       `json:"codec"`
	TimeBase             Fraction     `json:"timeBase"`
	ItemTrack            string       `json:"itemTrack"`
	EssenceStreamID      int          `json:"essenceStreamId"`
	Bitrate              int64        `json:"bitrate"`
	NumberOfPackets      int64        `json:"numberOfPackets"`
	Extradata            string       `json:"extradata"`
	Pid                  int          `json:"pid"`
	Duration             Timestamp    `json:"duration"`
	Profile              int          `json:"profile"`
	Level                int          `json:"level"`
	StartTimestamp       Timestamp    `json:"startTimestamp"`
}

type AudioComponent struct {
	ChannelCount    int       `json:"channelCount"`
	ChannelLayout   int64     `json:"channelLayout"`
	SampleFormat    string    `json:"sampleFormat"`
	FrameSize       int       `json:"frameSize"`
	MediaInfo       MediaInfo `json:"mediaInfo"`
	File            []File    `json:"file"`
	ID              string    `json:"id"`
	Metadata        []KV      `json:"metadata"`
	Codec           string    `json:"codec"`
	TimeBase        Fraction  `json:"timeBase"`
	ItemTrack       string    `json:"itemTrack"`
	EssenceStreamID int       `json:"essenceStreamId"`
	Bitrate         int64     `json:"bitrate"`
	Pid             int       `json:"pid"`
	Duration        Timestamp `json:"duration"`
	StartTimestamp  Timestamp `json:"startTimestamp"`
}

type BinaryComponent struct {
	Length   int64  `json:"length"`
	File     []File `json:"file"`
	ID       string `json:"id"`
	Metadata []KV   `json:"metadata"`
}
