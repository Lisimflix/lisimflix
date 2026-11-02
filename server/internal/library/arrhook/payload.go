package arrhook

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// event is what a notification asks for: its type and the paths whose
// files were added, replaced, renamed or deleted.
type event struct {
	Type  string   // eventType as sent
	Kind  string   // what we do: kindScan, kindTest or kindIgnore
	Paths []string // remote paths, for kindScan
}

const (
	kindScan   = "scan"
	kindTest   = "test"
	kindIgnore = "ignore"
)

// maxPaths bounds the paths one notification may name.
const maxPaths = 1000

// wire is the subset of the Sonarr and Radarr webhook payloads used here
// (their Notifications/Webhook classes on develop, October 2026). Both
// send camelCase JSON; Go matches field names case-insensitively anyway.
type wire struct {
	EventType string `json:"eventType"`
	Series    *struct {
		Path string `json:"path"`
	} `json:"series"`
	Movie *struct {
		FolderPath string `json:"folderPath"`
	} `json:"movie"`
	EpisodeFile  *wireFile  `json:"episodeFile"`
	EpisodeFiles []wireFile `json:"episodeFiles"`
	MovieFile    *wireFile  `json:"movieFile"`
	// deletedFiles is the replaced files on Download, but a boolean on
	// SeriesDelete/MovieDelete ("were the files deleted"): decoded only
	// when it's a list.
	DeletedFiles        json.RawMessage `json:"deletedFiles"`
	RenamedEpisodeFiles []wireRename    `json:"renamedEpisodeFiles"`
	RenamedMovieFiles   []wireRename    `json:"renamedMovieFiles"`
}

type wireFile struct {
	Path         string `json:"path"`
	RelativePath string `json:"relativePath"`
}

type wireRename struct {
	wireFile
	PreviousPath string `json:"previousPath"`
}

// Events that never touch files: acknowledged and ignored.
var ignored = map[string]bool{
	"grab": true, "health": true, "healthrestored": true, "applicationupdate": true,
	"manualinteractionrequired": true, "seriesadd": true, "movieadded": true,
}

var errSchema = errors.New("payload")

// parse reads a notification from a source of the given kind. A known event
// without the paths it should carry is an error, so a changed payload
// schema shows up as a failed notification in Sonarr or Radarr instead of
// silently doing nothing. Unknown event types are ignored, so a new event in
// a later release doesn't fail.
func parse(kind string, body []byte) (event, error) {
	var w wire
	if err := json.Unmarshal(body, &w); err != nil {
		return event{}, fmt.Errorf("%w: not JSON", errSchema)
	}
	e := event{Type: w.EventType, Kind: kindScan}
	t := strings.ToLower(w.EventType)
	var folder string
	if kind == Sonarr && w.Series != nil {
		folder = w.Series.Path
	}
	if kind == Radarr && w.Movie != nil {
		folder = w.Movie.FolderPath
	}
	file := func(f *wireFile) string {
		switch {
		case f == nil:
			return ""
		case f.Path != "":
			return f.Path
		case f.RelativePath != "" && folder != "":
			return strings.TrimRight(folder, `/\`) + "/" + f.RelativePath
		}
		return ""
	}
	add := func(p string) {
		if p != "" {
			e.Paths = append(e.Paths, p)
		}
	}
	switch {
	case t == "":
		return event{}, fmt.Errorf("%w: no eventType", errSchema)
	case t == "test":
		e.Kind = kindTest
		return e, nil
	case ignored[t]:
		e.Kind = kindIgnore
		return e, nil
	case t == "download":
		if kind == Sonarr {
			add(file(w.EpisodeFile))
			for i := range w.EpisodeFiles {
				add(file(&w.EpisodeFiles[i]))
			}
		} else {
			add(file(w.MovieFile))
		}
		if len(e.Paths) == 0 {
			return event{}, fmt.Errorf("%w: Download without a file path", errSchema)
		}
		var old []wireFile
		if len(w.DeletedFiles) > 0 && w.DeletedFiles[0] == '[' {
			if err := json.Unmarshal(w.DeletedFiles, &old); err != nil {
				return event{}, fmt.Errorf("%w: deletedFiles", errSchema)
			}
		}
		for i := range old {
			add(file(&old[i]))
		}
	case t == "rename":
		renamed := w.RenamedEpisodeFiles
		if kind == Radarr {
			renamed = w.RenamedMovieFiles
		}
		for i := range renamed {
			add(file(&renamed[i].wireFile))
			add(renamed[i].PreviousPath)
		}
		if len(e.Paths) == 0 {
			add(folder) // a rename with no file list: rescan the folder
		}
	case kind == Sonarr && t == "episodefiledelete", kind == Radarr && t == "moviefiledelete":
		f := w.EpisodeFile
		if kind == Radarr {
			f = w.MovieFile
		}
		add(file(f))
	case kind == Sonarr && t == "seriesdelete", kind == Radarr && t == "moviedelete":
		add(folder)
	default:
		e.Kind = kindIgnore
		return e, nil
	}
	if len(e.Paths) == 0 {
		return event{}, fmt.Errorf("%w: %s without a path", errSchema, w.EventType)
	}
	if len(e.Paths) > maxPaths {
		return event{}, fmt.Errorf("%w: more than %d paths", errSchema, maxPaths)
	}
	return e, nil
}

// Supported major versions, from the User-Agent both apps send
// ("Sonarr/4.0.20.3014 (ubuntu 22.04)"). A source whose app or major version
// isn't listed is refused, so a payload change in a new major release is a
// clear failure rather than a silent one.
var supported = map[string]map[int]bool{
	Sonarr: {4: true},
	Radarr: {5: true, 6: true},
}

var errVersion = errors.New("version")

// checkAgent returns the "App/version" product from ua if it matches the
// source's kind and a supported major version.
func checkAgent(kind, ua string) (string, error) {
	product, _, _ := strings.Cut(ua, " ")
	app, ver, ok := strings.Cut(product, "/")
	if !ok || !strings.EqualFold(app, kind) {
		return product, fmt.Errorf("%w: this source is %s, but the request came from %q", errVersion, kind, product)
	}
	majorText, _, _ := strings.Cut(ver, ".")
	major, err := strconv.Atoi(majorText)
	if err != nil || !supported[kind][major] {
		return product, fmt.Errorf("%w: %s isn't a supported version", errVersion, product)
	}
	return product, nil
}
