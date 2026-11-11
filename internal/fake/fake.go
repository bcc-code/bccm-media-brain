// Package fake builds realistic, seedable inputs for tests and dev-data scenarios. The same
// seed always produces the same values, so builders never read the clock.
package fake

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/brianvoe/gofakeit/v7"

	"github.com/bcc-media/mediabrain/internal/access"
	"github.com/bcc-media/mediabrain/internal/asset"
	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/file"
	"github.com/bcc-media/mediabrain/internal/project"
	"github.com/bcc-media/mediabrain/internal/subtitle"
	"github.com/bcc-media/mediabrain/pkg/vidispine"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

type Faker struct{ f *gofakeit.Faker }

func New(seed uint64) *Faker { return &Faker{f: gofakeit.New(seed)} }

func (x *Faker) Title() string { return x.f.MovieName() + " - " + x.f.Noun() }

// Pick returns one of the given values.
func Pick[T any](x *Faker, values ...T) T { return values[x.f.Number(0, len(values)-1)] }

func (x *Faker) Int(min, max int) int { return x.f.Number(min, max) }

func (x *Faker) sentence() string { return x.f.Sentence() }

func (x *Faker) metadata() json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"note": x.f.Phrase(), "season": x.f.Number(1, 9)})
	return raw
}

func (x *Faker) User() access.UserInput {
	first, last := x.f.FirstName(), x.f.LastName()
	return access.UserInput{
		ExternalID: "fake|" + x.f.UUID(),
		Email:      strings.ToLower(first+"."+last) + "." + strings.ToLower(x.f.LetterN(4)) + "@example.test",
		Name:       first + " " + last,
	}
}

func (x *Faker) ServiceAccount() access.ServiceAccountInput {
	return access.ServiceAccountInput{Name: strings.ToLower(x.f.Noun() + "-" + x.f.LetterN(4))}
}

// Read fills p from the seed, so a Faker can be the source of service tokens in dev data
// (access.Service.WithRand) and the same seed gives the same tokens.
func (x *Faker) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(x.f.Number(0, 255))
	}
	return len(p), nil
}

func (x *Faker) Program() project.ProgramInput {
	desc := x.sentence()
	return project.ProgramInput{
		Code: strings.ToUpper(x.f.LetterN(5)), Name: x.Title(), Description: &desc, Metadata: x.metadata(),
	}
}

// Project builds a project for the program with the given id and code.
func (x *Faker) Project(programID, programCode string) project.ProjectInput {
	status := Pick(x, sqlc.ProjectStatusPlanned, sqlc.ProjectStatusActive, sqlc.ProjectStatusDelivered, sqlc.ProjectStatusArchived)
	start := epoch.AddDate(0, 0, -x.f.Number(0, 700))
	end := start.AddDate(0, 0, x.f.Number(1, 120))
	return project.ProjectInput{
		ProgramID: programID,
		Code:      fmt.Sprintf("%s_S%02d_E%02d_%s", programCode, x.f.Number(1, 9), x.f.Number(1, 24), strings.ToUpper(x.f.LetterN(4))),
		Name:      x.Title(), Status: &status, StartsOn: &start, EndsOn: &end, Metadata: x.metadata(),
	}
}

func (x *Faker) Location() file.LocationInput {
	kind := Pick(x, sqlc.LocationKindNas, sqlc.LocationKindLto, sqlc.LocationKindS3, sqlc.LocationKindLocal)
	name := fmt.Sprintf("%s-%s-%d", kind, strings.ToLower(x.f.LetterN(5)), x.f.Number(10, 99))
	uri := map[sqlc.LocationKind]string{
		sqlc.LocationKindNas: "smb://nas01/" + name, sqlc.LocationKindLto: "lto://" + name,
		sqlc.LocationKindS3: "s3://" + name, sqlc.LocationKindLocal: "file:///mnt/" + name,
	}[kind]
	return file.LocationInput{Name: name, Kind: kind, BaseURI: uri, Online: kind != sqlc.LocationKindLto}
}

func (x *Faker) hash(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = x.f.Uint8()
	}
	return hex.EncodeToString(b)
}

// Hashes returns fresh xxh3_128 and sha256 hashes.
func (x *Faker) Hashes() []file.HashInput {
	return []file.HashInput{
		{Algo: sqlc.HashAlgoXxh3128, Value: x.hash(16)},
		{Algo: sqlc.HashAlgoSha256, Value: x.hash(32)},
	}
}

func (x *Faker) Path(ext string) string {
	return fmt.Sprintf("%s/%s/%s_%04d.%s", strings.ToLower(x.f.LetterN(6)), x.f.Noun(), strings.ToUpper(x.f.LetterN(4)), x.f.Number(1, 9999), ext)
}

// VideoFile builds the metadata of a camera or master video file, without copies.
func (x *Faker) VideoFile() file.RegisterInput {
	codec := Pick(x, "prores", "h264", "hevc", "dnxhd")
	mime := map[string]string{"prores": "video/quicktime", "dnxhd": "application/mxf"}[codec]
	if mime == "" {
		mime = "video/mp4"
	}
	res := Pick(x, [2]int32{1920, 1080}, [2]int32{3840, 2160}, [2]int32{1280, 720})
	duration := int64(x.f.Number(5_000, 3_600_000))
	probe, _ := json.Marshal(map[string]any{
		"format":  map[string]any{"format_name": strings.TrimPrefix(mime, "video/"), "duration": fmt.Sprintf("%.3f", float64(duration)/1000)},
		"streams": []map[string]any{{"codec_type": "video", "codec_name": codec, "width": res[0], "height": res[1], "r_frame_rate": "25/1"}},
	})
	return file.RegisterInput{
		SizeBytes: duration * int64(x.f.Number(500, 20_000)), MimeType: &mime, DurationMs: &duration,
		Codec: &codec, Width: &res[0], Height: &res[1], Probe: probe, Hashes: x.Hashes(),
	}
}

// SubtitleFile builds the metadata of a subtitle file holding the given text.
func (x *Faker) SubtitleFile(content string) file.RegisterInput {
	mime := "application/x-subrip"
	return file.RegisterInput{SizeBytes: int64(len(content)), MimeType: &mime, Hashes: x.Hashes()}
}

func (x *Faker) Asset(typeID int16) asset.Input {
	desc := x.sentence()
	captured := epoch.Add(-time.Duration(x.f.Number(1, 700*24)) * time.Hour)
	camera := Pick(x, "Sony FX6", "ARRI Alexa Mini", "Blackmagic URSA", "Canon C300")
	reel := fmt.Sprintf("A%03d", x.f.Number(1, 999))
	return asset.Input{
		TypeID: typeID, Title: x.Title(), Description: &desc, CapturedAt: &captured, Camera: &camera, Reel: &reel,
		Metadata: x.metadata(), LegacyIDs: []string{fmt.Sprintf("LEG-%06d", x.f.Number(1, 999999))},
	}
}

// Cues builds n cues that follow each other without overlapping.
func (x *Faker) Cues(n int) []subtitle.Cue {
	cues := make([]subtitle.Cue, n)
	at := int32(x.f.Number(0, 5000))
	for i := range cues {
		length := int32(x.f.Number(800, 5000))
		cues[i] = subtitle.Cue{StartMs: at, EndMs: at + length, Text: x.sentence()}
		at += length + int32(x.f.Number(0, 3000))
	}
	return cues
}

// SRT returns subtitle text with n cues.
func (x *Faker) SRT(n int) string {
	text, _ := subtitle.Render(subtitle.SRT, x.Cues(n))
	return text
}

// VTT returns WebVTT text with n cues, some of them with a speaker.
func (x *Faker) VTT(n int) string {
	cues := x.Cues(n)
	for i := range cues {
		if i%3 == 0 {
			speaker := x.f.FirstName()
			cues[i].Speaker = &speaker
		}
	}
	text, _ := subtitle.Render(subtitle.VTT, cues)
	return text
}

// VidispineItem is an item as the Vidispine API of a Cantemo Portal describes it.
type VidispineItem struct {
	ID     string
	Fields map[string]string
	Shapes []vidispine.Shape
}

// VidispineItem builds an item whose original file lies on the given Vidispine storage, with a
// low-resolution shape next to it.
func (x *Faker) VidispineItem(storageID string) VidispineItem {
	item := x.VidispinePlaceholder()
	path := "raw/" + x.Path("mxf")
	name := path[strings.LastIndex(path, "/")+1:]
	seconds := x.f.Number(5, 3600)
	res := Pick(x, vidispine.Resolution{Width: 1920, Height: 1080}, vidispine.Resolution{Width: 3840, Height: 2160})
	duration := vidispine.Timestamp{Samples: int64(seconds) * 25, TimeBase: vidispine.Fraction{Numerator: 1, Denominator: 25}}
	n := x.f.Number(100000, 999999)
	shape := func(tag, mime, codec, path string, size int64, res vidispine.Resolution) vidispine.Shape {
		n++
		return vidispine.Shape{
			ID: fmt.Sprintf("VX-%d", n), Tag: []string{tag}, MimeType: []string{mime},
			ContainerComponent: vidispine.ContainerComponent{Duration: duration, File: []vidispine.File{{
				ID: fmt.Sprintf("VX-%d", n+500000), Path: path, URI: []string{"file:///mnt/" + storageID + "/" + path},
				State: vidispine.FileStateClosed, Size: size, Hash: x.hash(20), Storage: storageID,
			}}},
			VideoComponent: []vidispine.VideoComponent{{Resolution: res, Codec: codec, Duration: duration}},
			AudioComponent: []vidispine.AudioComponent{{ChannelCount: 2, Codec: "pcm_s24le", Duration: duration}},
		}
	}
	item.Fields[vidispine.FieldOriginalFilename] = name
	item.Fields[vidispine.FieldDurationSeconds] = fmt.Sprint(seconds)
	item.Fields["originalVideoCodec"] = "mpeg2video"
	item.Shapes = []vidispine.Shape{
		shape(vidispine.ShapeTagOriginal, "application/mxf", "mpeg2video", path, int64(seconds)*int64(x.f.Number(500_000, 6_000_000)), res),
		shape(vidispine.ShapeTagLowres, "video/mp4", "h264", strings.TrimSuffix(path, ".mxf")+"_lowres.mp4", int64(seconds)*100_000, vidispine.Resolution{Width: 640, Height: 360}),
	}
	return item
}

// VidispinePlaceholder builds an item that has metadata but no files yet.
func (x *Faker) VidispinePlaceholder() VidispineItem {
	return VidispineItem{
		ID: fmt.Sprintf("VX-%d", x.f.Number(1000, 9_999_999)),
		Fields: map[string]string{
			vidispine.FieldTitle: x.Title(),
			"mediaType":          "video",
			"created":            epoch.Add(-time.Duration(x.f.Number(1, 700*24)) * time.Hour).Format(time.RFC3339),
			"user":               strings.ToLower(x.f.FirstName()),
			"portal_mf982016":    x.sentence(),
		},
	}
}
